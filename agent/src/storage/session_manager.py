"""Session (thread) metadata management.

Maintains the ``threads`` table shared with the gateway for session
listing, ownership, and title tracking.  Message history is read from
the LangGraph checkpointer — *not* duplicated into a separate table.

Architecture
~~~~~~~~~~~~
::

    user_id (JWT user)
      └── thread_id  (conversation 1)
      └── thread_id  (conversation 2)

The ``threads`` table is owned by the gateway ORM (``Thread`` model),
but both gateway and backend operate on the same Postgres database.
The backend writes to it directly via raw ``asyncpg`` queries so that
we don't need to duplicate the SQLAlchemy model.

Reference: agon-multi-agent ``backend/app/core/session_manager.py``
"""

from __future__ import annotations

import logging
from datetime import datetime
from typing import Any

from .database import get_checkpointer, get_db_connection

logger = logging.getLogger(__name__)

DEFAULT_LOCAL_USER_ID = "00000000-0000-0000-0000-000000000001"

THREADS_TABLE_SQL = """
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username VARCHAR(100) UNIQUE NOT NULL,
    display_name VARCHAR(200),
    email VARCHAR(255) UNIQUE,
    password_hash VARCHAR(255),
    avatar_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO users (id, username, display_name, email)
VALUES (
    '00000000-0000-0000-0000-000000000001'::uuid,
    'local-user',
    'Local User',
    'local-user@nous.local'
)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS threads (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    title TEXT,
    model_name TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    is_archived BOOLEAN DEFAULT FALSE
);

ALTER TABLE threads ADD COLUMN IF NOT EXISTS user_id UUID;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS title TEXT;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS model_name TEXT;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE threads ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE threads ADD COLUMN IF NOT EXISTS is_archived BOOLEAN DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_threads_user_updated
    ON threads(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_threads_user_archived
    ON threads(user_id, is_archived);
"""

_threads_table_ready = False


async def setup_threads_table() -> None:
    """Create the shared thread metadata table if it does not exist."""
    global _threads_table_ready
    if _threads_table_ready:
        return
    async with get_db_connection() as conn:
        await conn.execute(THREADS_TABLE_SQL)
    _threads_table_ready = True
    logger.info("threads table verified/created.")


class SessionManager:
    """Stateless helper for thread metadata CRUD + checkpoint-based history."""

    # ── Thread metadata (``threads`` table) ─────────────────────

    @classmethod
    async def create_session(
        cls,
        thread_id: str,
        user_id: str,
        title: str | None = None,
        model_name: str | None = None,
    ) -> dict[str, Any]:
        """Insert a new row into ``threads``."""
        await setup_threads_table()
        async with get_db_connection() as conn:
            row = await conn.fetchrow(
                """
                INSERT INTO threads (id, user_id, title, model_name, created_at, updated_at, is_archived)
                VALUES ($1::uuid, $2::uuid, $3, $4, NOW(), NOW(), false)
                ON CONFLICT (id) DO UPDATE SET
                    updated_at = NOW(),
                    title = COALESCE(EXCLUDED.title, threads.title),
                    model_name = COALESCE(EXCLUDED.model_name, threads.model_name)
                RETURNING id, title, model_name, created_at, updated_at, is_archived
                """,
                thread_id,
                user_id,
                title,
                model_name,
            )

        logger.info("create_session: thread_id=%s, user_id=%s", thread_id, user_id)
        return {
            "id": row["id"] if row else thread_id,
            "thread_id": thread_id,
            "user_id": user_id,
            "title": row["title"] if row else title,
            "model_name": row["model_name"] if row else model_name,
            "created_at": row["created_at"] if row else datetime.now(),
            "updated_at": row["updated_at"] if row else datetime.now(),
            "is_archived": row["is_archived"] if row else False,
            "message_count": 0,
        }

    @classmethod
    async def ensure_session_exists(
        cls,
        thread_id: str,
        user_id: str,
        title: str | None = None,
        model_name: str | None = None,
    ) -> None:
        """Create the session if it doesn't exist, otherwise touch ``updated_at``."""
        await setup_threads_table()
        async with get_db_connection() as conn:
            await conn.execute(
                """
                INSERT INTO threads (id, user_id, title, model_name, created_at, updated_at, is_archived)
                VALUES ($1::uuid, $2::uuid, $3, $4, NOW(), NOW(), false)
                ON CONFLICT (id) DO UPDATE SET updated_at = NOW()
                """,
                thread_id,
                user_id,
                title,
                model_name,
            )

    @classmethod
    async def update_title(cls, thread_id: str, title: str) -> None:
        """Set thread title (without bumping ``updated_at``)."""
        await setup_threads_table()
        async with get_db_connection() as conn:
            await conn.execute(
                "UPDATE threads SET title = $2 WHERE id = $1::uuid",
                thread_id,
                title,
            )

    @classmethod
    async def get_session(cls, thread_id: str) -> dict[str, Any] | None:
        await setup_threads_table()
        async with get_db_connection() as conn:
            row = await conn.fetchrow(
                """
                SELECT id, user_id, title, model_name, created_at, updated_at, is_archived
                FROM threads WHERE id = $1::uuid
                """,
                thread_id,
            )
            return dict(row) if row else None

    @classmethod
    async def list_sessions(
        cls,
        user_id: str,
        limit: int = 50,
        offset: int = 0,
        include_archived: bool = False,
    ) -> tuple[list[dict[str, Any]], int]:
        """Return ``(sessions, total)`` for a user, newest first."""
        await setup_threads_table()
        async with get_db_connection() as conn:
            archive_clause = "" if include_archived else "AND NOT is_archived"
            if user_id == DEFAULT_LOCAL_USER_ID:
                # Local gateway mode has no authenticated user. Older rows may
                # have been created with generated user ids, so show all local
                # thread metadata instead of hiding valid history.
                total = await conn.fetchval(
                    f"SELECT COUNT(*) FROM threads WHERE TRUE {archive_clause}",
                )
                rows = await conn.fetch(
                    f"""
                    SELECT id, user_id, title, model_name, created_at, updated_at, is_archived,
                           0::integer AS message_count
                    FROM threads
                    WHERE TRUE {archive_clause}
                    ORDER BY updated_at DESC
                    LIMIT $1 OFFSET $2
                    """,
                    limit,
                    offset,
                )
            else:
                total = await conn.fetchval(
                    f"SELECT COUNT(*) FROM threads WHERE user_id = $1::uuid {archive_clause}",
                    user_id,
                )
                rows = await conn.fetch(
                    f"""
                    SELECT id, user_id, title, model_name, created_at, updated_at, is_archived,
                           0::integer AS message_count
                    FROM threads
                    WHERE user_id = $1::uuid {archive_clause}
                    ORDER BY updated_at DESC
                    LIMIT $2 OFFSET $3
                    """,
                    user_id,
                    limit,
                    offset,
                )
            return [dict(r) for r in rows], total or 0

    @classmethod
    async def update_session(
        cls,
        thread_id: str,
        user_id: str,
        title: str | None = None,
        model_name: str | None = None,
        is_archived: bool | None = None,
    ) -> dict[str, Any] | None:
        """Update thread metadata and return the updated row."""
        await setup_threads_table()
        updates: list[str] = []
        values: list[Any] = [thread_id, user_id]

        if title is not None:
            values.append(title)
            updates.append(f"title = ${len(values)}")
        if model_name is not None:
            values.append(model_name)
            updates.append(f"model_name = ${len(values)}")
        if is_archived is not None:
            values.append(is_archived)
            updates.append(f"is_archived = ${len(values)}")

        if not updates:
            return await cls.get_session(thread_id)

        updates.append("updated_at = NOW()")
        async with get_db_connection() as conn:
            row = await conn.fetchrow(
                f"""
                UPDATE threads
                SET {", ".join(updates)}
                WHERE id = $1::uuid
                  AND (user_id = $2::uuid OR $2::uuid = '{DEFAULT_LOCAL_USER_ID}'::uuid)
                RETURNING id, user_id, title, model_name, created_at, updated_at, is_archived,
                          0::integer AS message_count
                """,
                *values,
            )
            return dict(row) if row else None

    @classmethod
    async def delete_session(cls, thread_id: str, user_id: str) -> bool:
        await setup_threads_table()
        async with get_db_connection() as conn:
            owner = await conn.fetchval(
                "SELECT user_id FROM threads WHERE id = $1::uuid",
                thread_id,
            )
            if owner is None:
                return False
            if str(owner) != user_id and user_id != DEFAULT_LOCAL_USER_ID:
                return False
            await conn.execute("DELETE FROM threads WHERE id = $1::uuid", thread_id)
            logger.info("delete_session: thread_id=%s", thread_id)
            return True

    # ── Conversation history from checkpointer ──────────────────

    @classmethod
    async def get_session_history(
        cls,
        thread_id: str,
        user_id: str | None = None,
        limit: int = 50,
    ) -> list[dict[str, Any]] | None:
        """Read messages from the latest checkpoint.

        Returns a simplified list of ``{id, role, content, timestamp}``
        dicts (human + ai only).  Returns ``None`` if the thread doesn't
        belong to *user_id* (permission denied).
        """
        if user_id is not None:
            session = await cls.get_session(thread_id)
            if not session or str(session.get("user_id")) != user_id:
                return None

        checkpointer = await get_checkpointer()
        config: dict[str, Any] = {"configurable": {"thread_id": thread_id}}

        # Build message-id → timestamp map from checkpoint history
        msg_ts_map: dict[str, str] = {}
        try:
            checkpoints: list[tuple[str, set[str]]] = []
            async for state in checkpointer.alist(config):
                cp = state.checkpoint
                ts = cp.get("ts", "")
                msgs = cp.get("channel_values", {}).get("messages", [])
                ids = {getattr(m, "id", None) for m in msgs if getattr(m, "id", None)}
                checkpoints.append((ts, ids))

            checkpoints.reverse()
            prev: set[str] = set()
            for ts, cur in checkpoints:
                for mid in cur - prev:
                    msg_ts_map[mid] = ts
                prev = cur
        except Exception:
            logger.warning("Failed to walk checkpoint history for timestamps", exc_info=True)

        # Latest checkpoint
        checkpoint_tuple = await checkpointer.aget_tuple(config)
        if not checkpoint_tuple or not checkpoint_tuple.checkpoint:
            return []

        cp = checkpoint_tuple.checkpoint
        fallback_ts = cp.get("ts", datetime.now().isoformat())
        messages = cp.get("channel_values", {}).get("messages", [])

        result: list[dict[str, Any]] = []
        for i, msg in enumerate(messages[-limit:]):
            msg_type = getattr(msg, "type", "unknown")
            if msg_type not in ("human", "ai"):
                continue

            content = getattr(msg, "content", "")
            image_urls: list[str] = []

            if isinstance(content, list):
                text_parts = []
                for part in content:
                    if isinstance(part, dict):
                        if part.get("type") == "text":
                            text_parts.append(part.get("text", ""))
                        elif part.get("type") == "image_url":
                            url = part.get("image_url", {}).get("url", "")
                            if url:
                                image_urls.append(url)
                content = "\n".join(text_parts)
            elif not isinstance(content, str):
                content = str(content)

            if msg_type == "ai" and not content.strip():
                continue

            msg_id = getattr(msg, "id", None) or f"msg_{i}"
            entry: dict[str, Any] = {
                "id": msg_id,
                "role": "user" if msg_type == "human" else "assistant",
                "content": content,
                "timestamp": msg_ts_map.get(msg_id, fallback_ts),
            }
            if image_urls:
                entry["image_urls"] = image_urls
            result.append(entry)

        return result
