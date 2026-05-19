"""CompactionMiddleware — triggers context compression before agent calls.

Sits in the middleware chain and transparently compacts history when the
estimated token count exceeds the configured threshold.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from typing import override

from langchain.agents import AgentState
from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import BaseMessage

from src.context.compaction import CompactionConfig, CompactionEngine

logger = logging.getLogger(__name__)


class CompactionMiddleware(AgentMiddleware[AgentState]):
    """Auto-compact conversation history when approaching the token budget."""

    def __init__(self, config: CompactionConfig | None = None):
        self._engine = CompactionEngine(config)

    def _maybe_compact(self, state: AgentState) -> AgentState:
        messages: list[BaseMessage] = state.get("messages", [])
        if not self._engine.should_compact(messages):
            return state

        result = self._engine.compact(messages)
        logger.info(
            "Compacted conversation: %d -> %d messages (removed %d, preserved %d)",
            result.original_count,
            len(result.compacted_messages),
            result.removed_count,
            result.preserved_count,
        )
        return {**state, "messages": result.compacted_messages}

    @override
    def call_model(
        self,
        state: AgentState,
        handler: Callable[[AgentState], AgentState],
    ) -> AgentState:
        state = self._maybe_compact(state)
        return handler(state)

    @override
    async def acall_model(
        self,
        state: AgentState,
        handler: Callable[[AgentState], Awaitable[AgentState]],
    ) -> AgentState:
        state = self._maybe_compact(state)
        return await handler(state)
