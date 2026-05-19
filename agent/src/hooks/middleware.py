"""HookMiddleware — injects pre/post-tool-use hooks into the middleware chain."""

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

from src.hooks.runner import HookRunner
from src.hooks.types import HookEvent

logger = logging.getLogger(__name__)


class HookMiddleware(AgentMiddleware[AgentState]):
    """Run pre/post-tool-use hooks around every tool call.

    - Pre hooks may deny execution or modify tool input.
    - Post hooks may inject additional feedback into the tool result.
    - A denial returns an error ToolMessage so the LLM can adapt.
    """

    def __init__(self, runner: HookRunner):
        self.runner = runner

    def _pre_check(self, request: ToolCallRequest) -> tuple[ToolCallRequest, ToolMessage | None]:
        """Run pre-tool-use hooks. Return possibly-modified request and optional denial."""
        tool_name = str(request.tool_call.get("name", ""))
        tool_input = request.tool_call.get("args", {})

        result = self.runner.run(HookEvent.PRE_TOOL_USE, tool_name, tool_input=tool_input)

        if result.is_denied():
            return request, self._denied_message(request, result.message or "denied by pre-tool-use hook")

        if result.updated_input is not None:
            updated_call = {**request.tool_call, "args": result.updated_input}
            request = ToolCallRequest(tool_call=updated_call, config=request.config)

        return request, None

    def _post_run(self, request: ToolCallRequest, result: ToolMessage | Command, error: Exception | None = None) -> ToolMessage | Command:
        """Run post-tool-use hooks and optionally merge feedback."""
        if not isinstance(result, ToolMessage):
            return result

        tool_name = str(request.tool_call.get("name", ""))
        tool_input = request.tool_call.get("args", {})
        is_error = result.status == "error" or error is not None

        event = HookEvent.POST_TOOL_USE_FAILURE if is_error else HookEvent.POST_TOOL_USE
        hook_result = self.runner.run(
            event,
            tool_name,
            tool_input=tool_input,
            tool_output=str(result.content) if result.content else None,
            is_error=is_error,
        )

        if hook_result.message:
            merged = f"{result.content}\n\n[Hook feedback] {hook_result.message}"
            return ToolMessage(
                content=merged,
                tool_call_id=result.tool_call_id,
                name=result.name,
                status="error" if hook_result.is_denied() else result.status,
            )
        return result

    @staticmethod
    def _denied_message(request: ToolCallRequest, reason: str) -> ToolMessage:
        tool_name = str(request.tool_call.get("name", "unknown_tool"))
        tool_call_id = str(request.tool_call.get("id", "missing_id"))
        return ToolMessage(
            content=f"Hook denied: {reason}. Choose an alternative approach.",
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
            request, denial = self._pre_check(request)
        except GraphBubbleUp:
            raise
        except Exception:
            logger.exception("Pre-tool hook error")
            request, denial = request, None

        if denial is not None:
            return denial

        error: Exception | None = None
        try:
            result = handler(request)
        except GraphBubbleUp:
            raise
        except Exception as exc:
            error = exc
            raise
        else:
            return self._post_run(request, result, error)

    @override
    async def awrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: Callable[[ToolCallRequest], Awaitable[ToolMessage | Command]],
    ) -> ToolMessage | Command:
        try:
            request, denial = self._pre_check(request)
        except GraphBubbleUp:
            raise
        except Exception:
            logger.exception("Pre-tool hook error (async)")
            request, denial = request, None

        if denial is not None:
            return denial

        error: Exception | None = None
        try:
            result = await handler(request)
        except GraphBubbleUp:
            raise
        except Exception as exc:
            error = exc
            raise
        else:
            return self._post_run(request, result, error)
