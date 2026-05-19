"""Data types for the hook system."""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum
from typing import Any, Literal


class HookEvent(StrEnum):
    PRE_TOOL_USE = "pre_tool_use"
    POST_TOOL_USE = "post_tool_use"
    POST_TOOL_USE_FAILURE = "post_tool_use_failure"
    SUBAGENT_START = "subagent_start"
    SUBAGENT_END = "subagent_end"


@dataclass(frozen=True)
class HookResult:
    """Aggregated result from running one or more hooks for an event."""

    outcome: Literal["allow", "deny", "warn"] = "allow"
    message: str | None = None
    updated_input: dict[str, Any] | None = None
    permission_behavior: Literal["allow", "ask", "deny"] | None = None
    prevent_continuation: bool = False
    additional_contexts: list[str] = field(default_factory=list)

    def is_denied(self) -> bool:
        return self.outcome == "deny"

    @classmethod
    def allowed(cls, message: str | None = None) -> HookResult:
        return cls(outcome="allow", message=message)

    @classmethod
    def denied(cls, message: str) -> HookResult:
        return cls(outcome="deny", message=message)

    @classmethod
    def warned(cls, message: str) -> HookResult:
        return cls(outcome="warn", message=message)


@dataclass
class HookConfig:
    """Configuration for a single hook entry."""

    command: str | None = None
    use: str | None = None
    tools: list[str] | None = None
    events: list[str] | None = None

    def matches_tool(self, tool_name: str) -> bool:
        if self.tools is None:
            return True
        return tool_name in self.tools

    def matches_event(self, event: HookEvent) -> bool:
        if self.events is None:
            return True
        return event.value in self.events


@dataclass
class HookPayload:
    """JSON payload passed to external hook processes via stdin."""

    event: str
    tool_name: str
    tool_input: dict[str, Any] | None = None
    tool_output: str | None = None
    is_error: bool = False
