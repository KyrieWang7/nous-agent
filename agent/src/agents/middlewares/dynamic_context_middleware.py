"""DynamicContextMiddleware - Prefix-Cache optimization via frozen-snapshot pattern.

In LLM API calls, keeping the System Prompt static enables high prefix-cache hit
rates (>95%), dramatically reducing TTFT (Time-To-First-Token) and token costs.

This middleware solves the problem of dynamic context (memory, current date) causing
system prompt changes that destroy cache reuse.

Strategy:
  1. System prompt is kept FULLY STATIC (no date, no memory).
  2. Dynamic context is injected ONCE as a hidden <system-reminder> HumanMessage
     inserted before the first user message (frozen-snapshot pattern).
  3. After injection, this message is "frozen" — it never changes, so all
     subsequent turns share the same message prefix → cache hit.
  4. If the conversation crosses midnight, a lightweight date-update reminder is
     injected before the current turn (not modifying the frozen snapshot).

ID-Swap Technique:
  The reminder takes the original first HumanMessage's ID so add_messages
  replaces it in-place (preserving position). The original user content gets
  a derived `{id}__user` ID appended immediately after.

Result:
  - System prompt: static, cacheable
  - Memory + date: injected once as frozen HumanMessage
  - Subsequent turns: hit prefix cache on system prompt + frozen snapshot
  - Midnight crossing: handled via minimal date-update, not full re-injection
"""

from __future__ import annotations

import logging
import re
import uuid
from datetime import datetime
from typing import Any, override

from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import HumanMessage
from langgraph.runtime import Runtime

logger = logging.getLogger(__name__)

_DATE_RE = re.compile(r"<current_date>([^<]+)</current_date>")
_DYNAMIC_CONTEXT_REMINDER_KEY = "dynamic_context_reminder"
_SUMMARY_MESSAGE_NAME = "summary"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _extract_date(content: str) -> str | None:
    """Return the first <current_date> value found in content, or None."""
    m = _DATE_RE.search(content)
    return m.group(1) if m else None


def is_dynamic_context_reminder(message: object) -> bool:
    """Return whether message is a hidden dynamic-context reminder."""
    return isinstance(message, HumanMessage) and bool(
        message.additional_kwargs.get(_DYNAMIC_CONTEXT_REMINDER_KEY)
    )


def _last_injected_date(messages: list) -> str | None:
    """Scan messages in reverse and return the most recently injected date.

    Uses the dynamic_context_reminder flag rather than content matching,
    so user messages containing <system-reminder> are not mistakenly treated.
    """
    for msg in reversed(messages):
        if is_dynamic_context_reminder(msg):
            content_str = msg.content if isinstance(msg.content, str) else str(msg.content)
            return _extract_date(content_str)
    return None


def _is_user_injection_target(message: object) -> bool:
    """Return whether message can receive a dynamic-context reminder before it."""
    return (
        isinstance(message, HumanMessage)
        and not is_dynamic_context_reminder(message)
        and message.name != _SUMMARY_MESSAGE_NAME
    )


# ---------------------------------------------------------------------------
# Middleware
# ---------------------------------------------------------------------------


class DynamicContextMiddleware(AgentMiddleware):
    """Inject memory and current date as a frozen <system-reminder> HumanMessage.

    First turn:
      Prepends a full system-reminder (memory + date) before the first HumanMessage.
      This message is then "frozen" for the whole session — its content never changes
      again, maximizing prefix-cache reuse across turns.

    Midnight crossing:
      If the conversation spans midnight, a lightweight date-update reminder is
      injected before the current turn's HumanMessage. This correction is persisted
      so subsequent turns see a consistent history.
    """

    def __init__(self, agent_name: str | None = None):
        """Initialize the middleware.

        Args:
            agent_name: If provided, loads per-agent memory context.
        """
        super().__init__()
        self._agent_name = agent_name

    def _get_memory_context(self) -> str:
        """Load user memory context for injection.

        Returns formatted memory string or empty string if unavailable.
        """
        try:
            from src.agents.memory.store import get_memory_store

            store = get_memory_store()
            if store is None:
                return ""

            # Try to get memory for the agent
            memory = store.get_memory(agent_name=self._agent_name)
            if memory:
                return f"<memory>\n{memory}\n</memory>"
        except Exception as e:
            logger.debug("Could not load memory context: %s", e)

        return ""

    def _build_full_reminder(self) -> str:
        """Build the complete system-reminder with memory + date."""
        memory_context = self._get_memory_context()
        current_date = datetime.now().strftime("%Y-%m-%d, %A")

        lines: list[str] = ["<system-reminder>"]
        if memory_context:
            lines.append(memory_context.strip())
            lines.append("")  # blank line separating memory from date
        lines.append(f"<current_date>{current_date}</current_date>")
        lines.append("</system-reminder>")

        return "\n".join(lines)

    def _build_date_update_reminder(self) -> str:
        """Build a minimal date-update reminder for midnight crossings."""
        current_date = datetime.now().strftime("%Y-%m-%d, %A")
        return "\n".join(
            [
                "<system-reminder>",
                f"<current_date>{current_date}</current_date>",
                "</system-reminder>",
            ]
        )

    @staticmethod
    def _make_reminder_and_user_messages(
        original: HumanMessage, reminder_content: str
    ) -> tuple[HumanMessage, HumanMessage]:
        """Return (reminder_msg, user_msg) using the ID-swap technique.

        reminder_msg takes the original message's ID so add_messages replaces
        it in-place (preserving position in the message list).
        user_msg carries the original content with a derived `{id}__user` ID
        and is appended immediately after by add_messages.
        """
        stable_id = original.id or str(uuid.uuid4())
        reminder_msg = HumanMessage(
            content=reminder_content,
            id=stable_id,
            additional_kwargs={
                "hide_from_ui": True,
                _DYNAMIC_CONTEXT_REMINDER_KEY: True,
            },
        )
        user_msg = HumanMessage(
            content=original.content,
            id=f"{stable_id}__user",
            name=original.name,
            additional_kwargs=original.additional_kwargs,
        )
        return reminder_msg, user_msg

    def _inject(self, state: Any) -> dict | None:
        """Core injection logic for both sync and async paths."""
        messages = list(state.get("messages", []))
        if not messages:
            return None

        current_date = datetime.now().strftime("%Y-%m-%d, %A")
        last_date = _last_injected_date(messages)

        logger.debug(
            "DynamicContextMiddleware: msg_count=%d last_date=%r current_date=%r",
            len(messages),
            last_date,
            current_date,
        )

        if last_date is None:
            # ── First turn: inject full reminder before first HumanMessage ──
            first_idx = next(
                (i for i, m in enumerate(messages) if _is_user_injection_target(m)),
                None,
            )
            if first_idx is None:
                return None

            full_reminder = self._build_full_reminder()
            logger.info(
                "DynamicContextMiddleware: injecting full reminder (len=%d) before msg id=%r",
                len(full_reminder),
                messages[first_idx].id,
            )
            reminder_msg, user_msg = self._make_reminder_and_user_messages(
                messages[first_idx], full_reminder
            )
            return {"messages": [reminder_msg, user_msg]}

        if last_date == current_date:
            # ── Same day: nothing to do (prefix cache intact) ──
            return None

        # ── Midnight crossed: inject date-update before current turn ──
        last_human_idx = next(
            (
                i
                for i in reversed(range(len(messages)))
                if _is_user_injection_target(messages[i])
            ),
            None,
        )
        if last_human_idx is None:
            return None

        reminder_msg, user_msg = self._make_reminder_and_user_messages(
            messages[last_human_idx], self._build_date_update_reminder()
        )
        logger.info(
            "DynamicContextMiddleware: midnight crossing — injected date update"
        )
        return {"messages": [reminder_msg, user_msg]}

    @override
    def before_agent(self, state: Any, runtime: Runtime) -> dict | None:
        return self._inject(state)

    @override
    async def abefore_agent(self, state: Any, runtime: Runtime) -> dict | None:
        return self._inject(state)
