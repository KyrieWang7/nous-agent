"""Permission policy engine — decides allow / deny / prompt for each tool call."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

from src.permissions.mode import PermissionMode

if TYPE_CHECKING:
    from src.permissions.prompter import PermissionPrompter

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class PermissionRequest:
    """Payload handed to a PermissionPrompter for interactive decision."""

    tool_name: str
    tool_input: Any
    required_mode: PermissionMode
    current_mode: PermissionMode


@dataclass(frozen=True)
class PermissionOutcome:
    """Result of a permission check."""

    allowed: bool
    reason: str | None = None

    @classmethod
    def allow(cls) -> PermissionOutcome:
        return cls(allowed=True)

    @classmethod
    def deny(cls, reason: str) -> PermissionOutcome:
        return cls(allowed=False, reason=reason)

    def is_denied(self) -> bool:
        return not self.allowed


@dataclass
class PermissionPolicy:
    """Maps tool names to required permission levels and authorises calls.

    Unknown tools default to DANGER_FULL_ACCESS (strictest practical level)
    so that unregistered tools are never silently allowed in restricted modes.
    """

    active_mode: PermissionMode = PermissionMode.ALLOW
    tool_requirements: dict[str, PermissionMode] = field(default_factory=dict)

    def with_tool_requirement(self, tool_name: str, mode: PermissionMode) -> PermissionPolicy:
        """Return a *new* policy with an additional tool requirement."""
        reqs = {**self.tool_requirements, tool_name: mode}
        return PermissionPolicy(active_mode=self.active_mode, tool_requirements=reqs)

    def required_mode_for(self, tool_name: str) -> PermissionMode:
        return self.tool_requirements.get(tool_name, PermissionMode.DANGER_FULL_ACCESS)

    def authorize(
        self,
        tool_name: str,
        tool_input: Any = None,
        prompter: PermissionPrompter | None = None,
    ) -> PermissionOutcome:
        required = self.required_mode_for(tool_name)

        if self.active_mode == PermissionMode.ALLOW or self.active_mode >= required:
            return PermissionOutcome.allow()

        if prompter is not None and self.active_mode == PermissionMode.PROMPT:
            request = PermissionRequest(
                tool_name=tool_name,
                tool_input=tool_input,
                required_mode=required,
                current_mode=self.active_mode,
            )
            decision = prompter.decide(request)
            if decision.allowed:
                return PermissionOutcome.allow()
            return PermissionOutcome.deny(reason=decision.reason or f"User denied permission for tool '{tool_name}'")

        return PermissionOutcome.deny(reason=(f"Tool '{tool_name}' requires {required.name} but session is in {self.active_mode.name} mode"))
