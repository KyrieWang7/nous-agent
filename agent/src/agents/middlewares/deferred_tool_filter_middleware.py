"""Middleware to filter deferred tool schemas from model binding.

When tool_search is enabled, deferred tools are registered but their schemas
should NOT be sent to the LLM via bind_tools (saving context tokens).
This middleware intercepts wrap_model_call and removes deferred tools from
request.tools so that model.bind_tools only receives active tool schemas.
"""

import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import override

from langchain.agents import AgentState
from langchain.agents.middleware import AgentMiddleware
from langchain.agents.middleware.types import ModelCallResult, ModelRequest, ModelResponse

logger = logging.getLogger(__name__)


@dataclass
class DeferredToolEntry:
    """A tool that has been deferred from model binding."""

    name: str
    description: str = ""


class DeferredToolRegistry:
    """Registry of tools whose schemas are deferred from model binding."""

    def __init__(self) -> None:
        self.entries: list[DeferredToolEntry] = []

    def register(self, name: str, description: str = "") -> None:
        self.entries.append(DeferredToolEntry(name=name, description=description))

    def __bool__(self) -> bool:
        return bool(self.entries)


_deferred_registry: DeferredToolRegistry | None = None


def get_deferred_registry() -> DeferredToolRegistry | None:
    """Return the global deferred tool registry, or None if not initialized."""
    return _deferred_registry


def set_deferred_registry(registry: DeferredToolRegistry) -> None:
    """Set the global deferred tool registry."""
    global _deferred_registry
    _deferred_registry = registry


class DeferredToolFilterMiddleware(AgentMiddleware[AgentState]):
    """Remove deferred tools from request.tools before model binding.

    ToolNode still holds all tools (including deferred) for execution routing,
    but the LLM only sees active tool schemas — deferred tools are discoverable
    via tool_search at runtime.
    """

    def _filter_tools(self, request: ModelRequest) -> ModelRequest:
        registry = get_deferred_registry()
        if not registry:
            return request

        deferred_names = {e.name for e in registry.entries}
        active_tools = [t for t in request.tools if getattr(t, "name", None) not in deferred_names]

        if len(active_tools) < len(request.tools):
            logger.debug(f"Filtered {len(request.tools) - len(active_tools)} deferred tool schema(s) from model binding")

        return request.override(tools=active_tools)

    @override
    def wrap_model_call(
        self,
        request: ModelRequest,
        handler: Callable[[ModelRequest], ModelResponse],
    ) -> ModelCallResult:
        return handler(self._filter_tools(request))

    @override
    async def awrap_model_call(
        self,
        request: ModelRequest,
        handler: Callable[[ModelRequest], Awaitable[ModelResponse]],
    ) -> ModelCallResult:
        return await handler(self._filter_tools(request))
