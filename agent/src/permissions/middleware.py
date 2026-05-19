"""PermissionMiddleware — enforces the layered permission model at tool-call time."""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from typing import override

from langchain.agents import AgentState
from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import ToolMessage
from langgraph.errors import GraphBubbleUp
from langgraph.prebuilt.tool_node import ToolCallRequest
from langgraph.types import Command

from src.permissions.policy import PermissionOutcome, PermissionPolicy
from src.permissions.prompter import PermissionPrompter

logger = logging.getLogger(__name__)


class PermissionMiddleware(AgentMiddleware[AgentState]):
    """Check each tool call against the session's PermissionPolicy.

    Denied calls are turned into error ``ToolMessage``s so the LLM can
    adapt rather than crashing.
    """

    def __init__(
        self,
        policy: PermissionPolicy,
        prompter: PermissionPrompter | None = None,
    ):
        self.policy = policy
        self.prompter = prompter

    def _check(self, request: ToolCallRequest) -> PermissionOutcome:
        tool_name = str(request.tool_call.get("name", ""))
        tool_input = request.tool_call.get("args", {})
        return self.policy.authorize(tool_name, tool_input, self.prompter)

    @staticmethod
    def _denied_message(request: ToolCallRequest, outcome: PermissionOutcome) -> ToolMessage:
        tool_name = str(request.tool_call.get("name", "unknown_tool"))
        tool_call_id = str(request.tool_call.get("id", "missing_id"))
        return ToolMessage(
            content=f"Permission denied: {outcome.reason}. Choose a different approach or request elevated permissions.",
            tool_call_id=tool_call_id,
            name=tool_name,
            status="error",
        )

    @override
    def wrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: Callable[[ToolCallRequest], ToolMessage | Command],
    ) -> ToolMessage | Command:
        try:
            outcome = self._check(request)
        except GraphBubbleUp:
            raise
        except Exception:
            logger.exception("Permission check failed unexpectedly, denying by default")
            outcome = PermissionOutcome.deny(reason="internal permission check error")

        if outcome.is_denied():
            logger.warning(
                "Permission denied: tool=%s reason=%s",
                request.tool_call.get("name"),
                outcome.reason,
            )
            return self._denied_message(request, outcome)
        return handler(request)

    @override
    async def awrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: Callable[[ToolCallRequest], Awaitable[ToolMessage | Command]],
    ) -> ToolMessage | Command:
        try:
            outcome = self._check(request)
        except GraphBubbleUp:
            raise
        except Exception:
            logger.exception("Permission check failed unexpectedly, denying by default")
            outcome = PermissionOutcome.deny(reason="internal permission check error")

        if outcome.is_denied():
            logger.warning(
                "Permission denied: tool=%s reason=%s",
                request.tool_call.get("name"),
                outcome.reason,
            )
            return self._denied_message(request, outcome)
        return await handler(request)
