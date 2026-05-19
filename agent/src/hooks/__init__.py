"""Hook governance layer for tool execution lifecycle.

Hooks provide programmable interception points before and after tool calls.
They can audit, modify inputs, deny execution, or inject feedback — without
bypassing the core permission model.
"""

from src.hooks.runner import HookRunner
from src.hooks.types import HookConfig, HookEvent, HookResult

__all__ = [
    "HookConfig",
    "HookEvent",
    "HookResult",
    "HookRunner",
]
