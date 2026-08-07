"""Memory updater — persists to PostgreSQL.

Enhanced with DeerFlow features:
- Correction/reinforcement hint injection into LLM prompt
- Upload mention stripping from LLM output
- Fact deduplication (case-folded content matching)
- Sync execution path (ThreadPoolExecutor-safe, no asyncio.run)
"""

import asyncio
import json
import logging
import re
import uuid
from typing import Any

import asyncpg

from src.agents.memory.prompt import (
    MEMORY_UPDATE_PROMPT,
    format_conversation_for_update,
)
from src.config.memory_config import get_memory_config
from src.models import create_chat_model
from src.storage.database import LANGGRAPH_PG_URI

logger = logging.getLogger(__name__)

# Matches sentences describing file-upload *events* (not general file work)
_UPLOAD_SENTENCE_RE = re.compile(
    r"[^.!?]*\b(?:"
    r"upload(?:ed|ing)?(?:\s+\w+){0,3}\s+(?:file|files?|document|documents?|attachment|attachments?)"
    r"|file\s+upload"
    r"|/mnt/user-data/uploads/"
    r"|<uploaded_files>"
    r")[^.!?]*[.!?]?\s*",
    re.IGNORECASE,
)


async def _connect() -> asyncpg.Connection:
    return await asyncpg.connect(LANGGRAPH_PG_URI)


async def _get_user_id_by_thread(conn: asyncpg.Connection, thread_id: str) -> str | None:
    val = await conn.fetchval(
        "SELECT user_id FROM threads WHERE id = $1::uuid", thread_id,
    )
    return str(val) if val else None


async def _read_memory(conn: asyncpg.Connection, user_id: str) -> dict[str, Any]:
    """Read memory from PostgreSQL in legacy JSON format."""
    section_rows = await conn.fetch(
        "SELECT section_key, summary, updated_at "
        "FROM user_memory_sections WHERE user_id = $1::uuid "
        "ORDER BY section_key",
        user_id,
    )
    fact_rows = await conn.fetch(
        "SELECT id, content, category, confidence, source, created_at "
        "FROM user_memory_facts WHERE user_id = $1::uuid "
        "ORDER BY confidence DESC",
        user_id,
    )

    user_ctx: dict[str, Any] = {}
    history_ctx: dict[str, Any] = {}
    last_updated = ""

    for row in section_rows:
        updated = row["updated_at"].isoformat() + "Z"
        if updated > last_updated:
            last_updated = updated
        data = {"summary": row["summary"], "updatedAt": updated}
        key = row["section_key"]
        if key.startswith("user."):
            user_ctx[key.split(".", 1)[1]] = data
        elif key.startswith("history."):
            history_ctx[key.split(".", 1)[1]] = data

    for k in ("workContext", "personalContext", "topOfMind"):
        user_ctx.setdefault(k, {"summary": "", "updatedAt": ""})
    for k in ("recentMonths", "earlierContext", "longTermBackground"):
        history_ctx.setdefault(k, {"summary": "", "updatedAt": ""})

    facts = [
        {
            "id": r["id"],
            "content": r["content"],
            "category": r["category"],
            "confidence": float(r["confidence"]),
            "createdAt": r["created_at"].isoformat() + "Z",
            "source": r["source"] or "unknown",
        }
        for r in fact_rows
    ]

    return {
        "version": "1.0",
        "lastUpdated": last_updated,
        "user": user_ctx,
        "history": history_ctx,
        "facts": facts,
    }


async def _write_memory(
    conn: asyncpg.Connection,
    user_id: str,
    memory_data: dict[str, Any],
    *,
    facts_to_remove: set[str] | None = None,
    new_facts: list[dict[str, Any]] | None = None,
) -> None:
    """Persist memory updates into PostgreSQL."""
    async with conn.transaction():
        for prefix, section_map in (
            ("user", memory_data.get("user", {})),
            ("history", memory_data.get("history", {})),
        ):
            for sub_key, val in section_map.items():
                summary = val.get("summary", "") if isinstance(val, dict) else ""
                if not summary:
                    continue
                section_key = f"{prefix}.{sub_key}"
                await conn.execute(
                    """
                    INSERT INTO user_memory_sections
                           (user_id, section_key, summary, updated_at)
                    VALUES ($1::uuid, $2, $3, NOW())
                    ON CONFLICT (user_id, section_key)
                    DO UPDATE SET summary = $3, updated_at = NOW()
                    """,
                    user_id, section_key, summary,
                )

        if facts_to_remove:
            await conn.execute(
                "DELETE FROM user_memory_facts "
                "WHERE user_id = $1::uuid AND id = ANY($2::text[])",
                user_id, list(facts_to_remove),
            )

        if new_facts:
            for fact in new_facts:
                await conn.execute(
                    """
                    INSERT INTO user_memory_facts
                           (id, user_id, content, category, confidence, source, created_at)
                    VALUES ($1, $2::uuid, $3, $4, $5, $6, NOW())
                    ON CONFLICT (id) DO UPDATE
                    SET content = $3, category = $4, confidence = $5
                    """,
                    fact["id"], user_id,
                    fact["content"],
                    fact.get("category", "context"),
                    fact.get("confidence", 0.5),
                    fact.get("source"),
                )


async def get_memory_data(user_id: str) -> dict[str, Any]:
    """Read the current memory from PostgreSQL for *user_id*."""
    conn = await _connect()
    try:
        return await _read_memory(conn, user_id)
    finally:
        await conn.close()


def _extract_text(content: Any) -> str:
    """Extract plain text from LLM response content (str or list of blocks)."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        pieces: list[str] = []
        for block in content:
            if isinstance(block, str):
                pieces.append(block)
            elif isinstance(block, dict):
                text_val = block.get("text")
                if isinstance(text_val, str):
                    pieces.append(text_val)
        return "\n".join(pieces)
    return str(content)


def _parse_update_data_from_response(content: Any) -> dict[str, Any] | None:
    """Extract the first JSON object from plain, fenced, or wrapped LLM output."""
    text = _extract_text(content).strip()
    decoder = json.JSONDecoder()
    for match in re.finditer(r"\{", text):
        try:
            value, _ = decoder.raw_decode(text[match.start():])
        except json.JSONDecodeError:
            continue
        if isinstance(value, dict):
            return value
    return None


def _strip_upload_mentions_from_memory(memory_data: dict[str, Any]) -> dict[str, Any]:
    """Remove sentences about file uploads from all memory summaries and facts.

    Uploaded files are session-scoped; persisting upload events causes the
    agent to search for non-existent files in future sessions.
    """
    for section in ("user", "history"):
        section_data = memory_data.get(section, {})
        for _key, val in section_data.items():
            if isinstance(val, dict) and "summary" in val:
                cleaned = _UPLOAD_SENTENCE_RE.sub("", val["summary"]).strip()
                cleaned = re.sub(r"  +", " ", cleaned)
                val["summary"] = cleaned

    facts = memory_data.get("facts", [])
    if facts:
        memory_data["facts"] = [f for f in facts if not _UPLOAD_SENTENCE_RE.search(f.get("content", ""))]

    return memory_data


def _fact_content_key(content: Any) -> str | None:
    """Normalize fact content for deduplication (case-folded)."""
    if not isinstance(content, str):
        return None
    stripped = content.strip()
    return stripped.casefold() if stripped else None


class MemoryUpdater:
    """Updates memory using LLM based on conversation context."""

    def __init__(self, model_name: str | None = None):
        self._model_name = model_name

    def _get_model(self):
        config = get_memory_config()
        model_name = self._model_name or config.model_name
        return create_chat_model(name=model_name, thinking_enabled=False)

    def _build_correction_hint(
        self,
        correction_detected: bool,
        reinforcement_detected: bool,
    ) -> str:
        """Build optional prompt hints for correction and reinforcement signals."""
        parts = []
        if correction_detected:
            parts.append(
                "IMPORTANT: 检测到用户纠正信号。请特别注意 Agent 哪里做错了、用户纠正了什么，"
                "将正确方法记录为 category=\"correction\"、confidence >= 0.95 的 fact。"
                "如果错误很明确，在 sourceError 字段说明之前的错误做法。"
            )
        if reinforcement_detected:
            parts.append(
                "IMPORTANT: 检测到用户正面确认信号。用户明确确认了 Agent 的方法是正确的。"
                "将确认的方法/风格/偏好记录为 category=\"preference\" 或 \"behavior\"、"
                "confidence >= 0.9 的 fact。"
            )
        return "\n".join(parts)

    def update_memory_sync(
        self,
        messages: list[Any],
        thread_id: str | None = None,
        correction_detected: bool = False,
        reinforcement_detected: bool = False,
    ) -> bool:
        """Synchronous memory update using model.invoke() (no event loop needed).

        This is the primary path called from the ThreadPoolExecutor in the queue.
        Uses sync HTTP calls to avoid cross-loop connection pool issues.
        """
        config = get_memory_config()
        if not config.enabled or not messages or not thread_id:
            return False

        try:
            # Bridge to async for DB operations only (lightweight, no LLM)
            conn = asyncio.run(asyncpg.connect(LANGGRAPH_PG_URI))
            try:
                user_id = asyncio.run(_get_user_id_by_thread(conn, thread_id))
                if not user_id:
                    logger.warning("No user_id found for thread %s", thread_id)
                    return False

                current_memory = asyncio.run(_read_memory(conn, user_id))
            finally:
                asyncio.run(conn.close())

            conversation_text = format_conversation_for_update(messages)
            if not conversation_text.strip():
                return False

            correction_hint = self._build_correction_hint(correction_detected, reinforcement_detected)
            prompt = MEMORY_UPDATE_PROMPT.format(
                current_memory=json.dumps(current_memory, indent=2),
                conversation=conversation_text,
                correction_hint=correction_hint,
            )

            # Sync LLM call — safe for ThreadPoolExecutor
            model = self._get_model()
            response = model.invoke(prompt)
            update_data = _parse_update_data_from_response(response.content)
            if update_data is None:
                raise json.JSONDecodeError("no JSON object found", _extract_text(response.content), 0)

            updated_memory, facts_to_remove, new_facts = self._apply_updates(
                current_memory, update_data, thread_id
            )

            # Strip upload mentions from LLM output
            updated_memory = _strip_upload_mentions_from_memory(updated_memory)

            # Write back to PostgreSQL
            conn = asyncio.run(asyncpg.connect(LANGGRAPH_PG_URI))
            try:
                asyncio.run(_write_memory(
                    conn, user_id, updated_memory,
                    facts_to_remove=facts_to_remove,
                    new_facts=new_facts,
                ))
            finally:
                asyncio.run(conn.close())

            logger.info("Memory persisted for user %s (thread %s)", user_id, thread_id)
            return True

        except json.JSONDecodeError:
            logger.warning("Failed to parse LLM response for memory update", exc_info=True)
            return False
        except Exception:
            logger.error("Memory update failed", exc_info=True)
            return False

    async def update_memory(
        self,
        messages: list[Any],
        thread_id: str | None = None,
        correction_detected: bool = False,
        reinforcement_detected: bool = False,
    ) -> bool:
        """Async memory update (for direct async callers)."""
        config = get_memory_config()
        if not config.enabled or not messages or not thread_id:
            return False

        conn = await _connect()
        try:
            user_id = await _get_user_id_by_thread(conn, thread_id)
            if not user_id:
                logger.warning("No user_id found for thread %s", thread_id)
                return False

            current_memory = await _read_memory(conn, user_id)
            conversation_text = format_conversation_for_update(messages)
            if not conversation_text.strip():
                return False

            correction_hint = self._build_correction_hint(correction_detected, reinforcement_detected)
            prompt = MEMORY_UPDATE_PROMPT.format(
                current_memory=json.dumps(current_memory, indent=2),
                conversation=conversation_text,
                correction_hint=correction_hint,
            )

            model = self._get_model()
            response = model.invoke(prompt)
            update_data = _parse_update_data_from_response(response.content)
            if update_data is None:
                raise json.JSONDecodeError("no JSON object found", _extract_text(response.content), 0)

            updated_memory, facts_to_remove, new_facts = self._apply_updates(
                current_memory, update_data, thread_id
            )
            updated_memory = _strip_upload_mentions_from_memory(updated_memory)

            await _write_memory(
                conn, user_id, updated_memory,
                facts_to_remove=facts_to_remove,
                new_facts=new_facts,
            )
            logger.info("Memory persisted for user %s (thread %s)", user_id, thread_id)
            return True

        except json.JSONDecodeError:
            logger.warning("Failed to parse LLM response for memory update", exc_info=True)
            return False
        except Exception:
            logger.error("Memory update failed", exc_info=True)
            return False
        finally:
            await conn.close()

    def _apply_updates(
        self,
        current_memory: dict[str, Any],
        update_data: dict[str, Any],
        thread_id: str | None = None,
    ) -> tuple[dict[str, Any], set[str], list[dict[str, Any]]]:
        """Apply LLM-generated updates with fact deduplication."""
        config = get_memory_config()

        user_updates = update_data.get("user", {})
        for section in ("workContext", "personalContext", "topOfMind"):
            section_data = user_updates.get(section, {})
            if section_data.get("shouldUpdate") and section_data.get("summary"):
                current_memory["user"][section] = {
                    "summary": section_data["summary"],
                    "updatedAt": "",
                }

        history_updates = update_data.get("history", {})
        for section in ("recentMonths", "earlierContext", "longTermBackground"):
            section_data = history_updates.get(section, {})
            if section_data.get("shouldUpdate") and section_data.get("summary"):
                current_memory["history"][section] = {
                    "summary": section_data["summary"],
                    "updatedAt": "",
                }

        facts_to_remove = set(update_data.get("factsToRemove", []))

        # Build existing fact keys for deduplication
        existing_fact_keys = {
            key for key in (
                _fact_content_key(fact.get("content"))
                for fact in current_memory.get("facts", [])
            ) if key is not None
        }

        new_facts: list[dict[str, Any]] = []
        for fact in update_data.get("newFacts", []):
            confidence = fact.get("confidence", 0.5)
            if confidence < config.fact_confidence_threshold:
                continue

            raw_content = fact.get("content", "")
            if not isinstance(raw_content, str):
                continue
            normalized_content = raw_content.strip()
            if not normalized_content:
                continue

            # Deduplication check
            fact_key = _fact_content_key(normalized_content)
            if fact_key is not None and fact_key in existing_fact_keys:
                continue

            fact_entry: dict[str, Any] = {
                "id": f"fact_{uuid.uuid4().hex[:8]}",
                "content": normalized_content,
                "category": fact.get("category", "context"),
                "confidence": confidence,
                "source": thread_id or "unknown",
            }
            # Preserve sourceError for correction facts
            source_error = fact.get("sourceError")
            if isinstance(source_error, str) and source_error.strip():
                fact_entry["sourceError"] = source_error.strip()

            new_facts.append(fact_entry)
            if fact_key is not None:
                existing_fact_keys.add(fact_key)

        # Enforce max facts limit
        all_facts = [f for f in current_memory.get("facts", []) if f.get("id") not in facts_to_remove]
        all_facts.extend(new_facts)
        if len(all_facts) > config.max_facts:
            all_facts = sorted(all_facts, key=lambda f: f.get("confidence", 0), reverse=True)[:config.max_facts]

        return current_memory, facts_to_remove, new_facts


async def update_memory_from_conversation(
    messages: list[Any],
    thread_id: str | None = None,
    correction_detected: bool = False,
    reinforcement_detected: bool = False,
) -> bool:
    """Convenience async wrapper."""
    updater = MemoryUpdater()
    return await updater.update_memory(
        messages, thread_id,
        correction_detected=correction_detected,
        reinforcement_detected=reinforcement_detected,
    )
