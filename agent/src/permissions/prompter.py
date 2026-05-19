"""PermissionPrompter abstraction — decouples policy from UI/transport."""

from __future__ import annotations

import abc
from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from src.permissions.policy import PermissionRequest


@dataclass(frozen=True)
class PromptDecision:
    """Decision returned by a PermissionPrompter."""

    allowed: bool
    reason: str | None = None


class PermissionPrompter(abc.ABC):
    """Interface for interactive permission approval.

    Implementations may prompt via CLI stdin, HTTP callback, WebSocket, etc.
    """

    @abc.abstractmethod
    def decide(self, request: PermissionRequest) -> PromptDecision:
        """Return an allow/deny decision for *request*."""

    @abc.abstractmethod
    async def adecide(self, request: PermissionRequest) -> PromptDecision:
        """Async variant of :meth:`decide`."""


class AutoAllowPrompter(PermissionPrompter):
    """Always allows — useful for testing or fully-trusted environments."""

    def decide(self, request: PermissionRequest) -> PromptDecision:
        return PromptDecision(allowed=True)

    async def adecide(self, request: PermissionRequest) -> PromptDecision:
        return PromptDecision(allowed=True)


class AutoDenyPrompter(PermissionPrompter):
    """Always denies — useful for strict headless execution."""

    def decide(self, request: PermissionRequest) -> PromptDecision:
        return PromptDecision(allowed=False, reason="auto-deny policy")

    async def adecide(self, request: PermissionRequest) -> PromptDecision:
        return PromptDecision(allowed=False, reason="auto-deny policy")
