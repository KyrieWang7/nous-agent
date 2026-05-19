"""Deterministic context compaction engine.

Compresses the middle of a conversation into a structured summary while
preserving the most recent messages verbatim. Handles re-compaction of
already-summarised conversations (merge, not overwrite).

Inspired by claw-code's ``compact.rs``.
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass

from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage, ToolMessage

logger = logging.getLogger(__name__)

_CHARS_PER_TOKEN_ESTIMATE = 4
_SUMMARY_TAG = "<compacted_summary>"
_SUMMARY_END_TAG = "</compacted_summary>"


@dataclass
class CompactionConfig:
    max_estimated_tokens: int = 80_000
    preserve_recent_messages: int = 6
    max_timeline_entries: int = 40
    summary_line_budget: int = 160


@dataclass
class CompactionResult:
    compacted_messages: list[BaseMessage]
    summary_text: str
    original_count: int
    preserved_count: int
    removed_count: int


class CompactionEngine:
    """Stateless compaction: call ``compact`` each time you want to check / apply."""

    def __init__(self, config: CompactionConfig | None = None):
        self.config = config or CompactionConfig()

    def estimate_tokens(self, messages: list[BaseMessage]) -> int:
        total_chars = sum(len(str(m.content)) for m in messages)
        return total_chars // _CHARS_PER_TOKEN_ESTIMATE + 1

    def should_compact(self, messages: list[BaseMessage]) -> bool:
        non_summary = [m for m in messages if not _is_summary_message(m)]
        if len(non_summary) < self.config.preserve_recent_messages + 2:
            return False
        return self.estimate_tokens(non_summary) > self.config.max_estimated_tokens

    def compact(self, messages: list[BaseMessage]) -> CompactionResult:
        """Compress *messages* by summarising the middle and preserving the tail."""
        if not messages:
            return CompactionResult(
                compacted_messages=[],
                summary_text="",
                original_count=0,
                preserved_count=0,
                removed_count=0,
            )

        existing_summary = _extract_existing_summary(messages)

        preserve_n = self.config.preserve_recent_messages
        if len(messages) <= preserve_n:
            return CompactionResult(
                compacted_messages=list(messages),
                summary_text="",
                original_count=len(messages),
                preserved_count=len(messages),
                removed_count=0,
            )

        preserved = messages[-preserve_n:]
        to_summarise = messages[:-preserve_n]

        if existing_summary:
            to_summarise = [m for m in to_summarise if not _is_summary_message(m)]

        summary_text = self._build_summary(to_summarise, existing_summary)

        continuation = _get_continuation_message(summary_text, bool(preserved))

        compacted: list[BaseMessage] = [
            SystemMessage(content=continuation),
            *preserved,
        ]

        return CompactionResult(
            compacted_messages=compacted,
            summary_text=summary_text,
            original_count=len(messages),
            preserved_count=len(preserved),
            removed_count=len(to_summarise),
        )

    def _build_summary(self, messages: list[BaseMessage], existing_summary: str | None) -> str:
        """Build a deterministic structured summary."""
        tool_names: set[str] = set()
        user_requests: list[str] = []
        key_paths: set[str] = set()
        timeline: list[str] = []

        for msg in messages:
            content_str = str(msg.content)
            if isinstance(msg, HumanMessage):
                snippet = content_str[: self.config.summary_line_budget].strip()
                if snippet:
                    user_requests.append(snippet)

            elif isinstance(msg, ToolMessage):
                name = getattr(msg, "name", None) or "unknown_tool"
                tool_names.add(name)

            elif isinstance(msg, AIMessage):
                if hasattr(msg, "tool_calls") and msg.tool_calls:
                    for tc in msg.tool_calls:
                        tool_names.add(tc.get("name", "unknown"))

            paths = _extract_paths(content_str)
            key_paths.update(paths)

            if len(timeline) < self.config.max_timeline_entries:
                role = type(msg).__name__.replace("Message", "")
                entry = f"[{role}] {content_str[:120].strip()}"
                timeline.append(entry)

        parts: list[str] = []

        if existing_summary:
            parts.append(f"[Previously] {existing_summary}")

        if user_requests:
            parts.append("[User requests] " + " | ".join(user_requests[:10]))

        if tool_names:
            parts.append(f"[Tools used] {', '.join(sorted(tool_names))}")

        if key_paths:
            parts.append(f"[Key paths] {', '.join(sorted(key_paths)[:20])}")

        if timeline:
            parts.append("[Timeline]\n" + "\n".join(timeline))

        return "\n".join(parts)


def _get_continuation_message(summary: str, has_preserved: bool) -> str:
    """Build the system continuation message that prevents the model from re-introducing itself."""
    preamble = (
        "The conversation history has been compacted to save context space. "
        "Below is a structured summary of the earlier conversation. "
        "Continue the task directly — do NOT re-introduce yourself, "
        "do NOT summarise what happened, and do NOT repeat greetings."
    )
    if has_preserved:
        preamble += " The most recent messages follow the summary verbatim."

    return f"{preamble}\n\n{_SUMMARY_TAG}\n{summary}\n{_SUMMARY_END_TAG}"


def _is_summary_message(msg: BaseMessage) -> bool:
    return isinstance(msg, SystemMessage) and _SUMMARY_TAG in str(msg.content)


def _extract_existing_summary(messages: list[BaseMessage]) -> str | None:
    for msg in messages:
        if _is_summary_message(msg):
            content = str(msg.content)
            start = content.find(_SUMMARY_TAG)
            end = content.find(_SUMMARY_END_TAG)
            if start != -1 and end != -1:
                return content[start + len(_SUMMARY_TAG) : end].strip()
    return None


_PATH_RE = re.compile(r"(?:/[\w._-]+){2,}")


def _extract_paths(text: str) -> set[str]:
    return set(_PATH_RE.findall(text)[:30])
