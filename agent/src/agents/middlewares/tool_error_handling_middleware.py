"""Tool error handling middleware."""

import logging
from collections.abc import Awaitable, Callable
from typing import override

from langchain.agents import AgentState
from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import ToolMessage
from langgraph.errors import GraphBubbleUp
from langgraph.prebuilt.tool_node import ToolCallRequest
from langgraph.types import Command

logger = logging.getLogger(__name__)

_MISSING_TOOL_CALL_ID = "missing_tool_call_id"
_TASK_TOOL_NAME = "task"


def _stamp_task_subagent_status(message: ToolMessage, *, tool_name: str, error: str | None = None) -> ToolMessage:
    """Centralised stamping of ``additional_kwargs.subagent_status``.

    The mapping from the task tool's return string to a structured status is the
    single source of truth shared with the frontend (see
    ``contracts/subagent_status_contract.json``). Enforcing it here — the one place
    every task tool result flows through — keeps backend and frontend from drifting.

    For non-``task`` tools this is a no-op so other tools' additional_kwargs are
    left untouched.
    """
    if tool_name != _TASK_TOOL_NAME:
        return message

    # Imported lazily: ``src.subagents`` eagerly imports the executor, which pulls
    # in the agent/middleware graph — importing it at module top would create a
    # circular import. ``status_contract`` itself has no heavy deps.
    from src.subagents.status_contract import (
        extract_subagent_status,
        make_subagent_additional_kwargs,
    )

    content = message.content if isinstance(message.content, str) else str(message.content)
    status = extract_subagent_status(content)
    if status is None:
        return message

    stamp = make_subagent_additional_kwargs(status, error=error)
    merged = {**(message.additional_kwargs or {}), **stamp}
    return message.model_copy(update={"additional_kwargs": merged})


class ToolErrorHandlingMiddleware(AgentMiddleware[AgentState]):
    """Convert tool exceptions into error ToolMessages so the run can continue.

    Also stamps the structured ``subagent_status`` contract onto ``task`` tool
    results (both success and error paths) so the frontend can read a structured
    field instead of string-matching the result text.
    """

    def _build_error_message(self, request: ToolCallRequest, exc: Exception) -> ToolMessage:
        tool_name = str(request.tool_call.get("name") or "unknown_tool")
        tool_call_id = str(request.tool_call.get("id") or _MISSING_TOOL_CALL_ID)
        detail = str(exc).strip() or exc.__class__.__name__
        if len(detail) > 500:
            detail = detail[:497] + "..."

        content = f"Error: Tool '{tool_name}' failed with {exc.__class__.__name__}: {detail}. Continue with available context, or choose an alternative tool."
        message = ToolMessage(
            content=content,
            tool_call_id=tool_call_id,
            name=tool_name,
            status="error",
        )
        # The wrapped task exception serialises to ``Error: Tool 'task' failed ...``
        # on the wire; stamp the structured status so the frontend card resolves to
        # "failed" without parsing that string.
        return _stamp_task_subagent_status(message, tool_name=tool_name, error=detail)

    @staticmethod
    def _maybe_stamp(result: ToolMessage | Command, request: ToolCallRequest) -> ToolMessage | Command:
        """Apply the subagent stamp to successful task tool returns."""
        if not isinstance(result, ToolMessage):
            return result
        tool_name = str(request.tool_call.get("name") or result.name or "unknown_tool")
        return _stamp_task_subagent_status(result, tool_name=tool_name)

    @override
    def wrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: Callable[[ToolCallRequest], ToolMessage | Command],
    ) -> ToolMessage | Command:
        try:
            result = handler(request)
        except GraphBubbleUp:
            raise
        except Exception as exc:
            logger.exception("Tool execution failed (sync): name=%s id=%s", request.tool_call.get("name"), request.tool_call.get("id"))
            return self._build_error_message(request, exc)
        return self._maybe_stamp(result, request)

    @override
    async def awrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: Callable[[ToolCallRequest], Awaitable[ToolMessage | Command]],
    ) -> ToolMessage | Command:
        try:
            result = await handler(request)
        except GraphBubbleUp:
            raise
        except Exception as exc:
            logger.exception("Tool execution failed (async): name=%s id=%s", request.tool_call.get("name"), request.tool_call.get("id"))
            return self._build_error_message(request, exc)
        return self._maybe_stamp(result, request)
