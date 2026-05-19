"""Permission mode hierarchy for tool access control.

Modes form a total order: higher modes subsume lower ones.
A tool requiring WORKSPACE_WRITE is automatically allowed when the session
runs in DANGER_FULL_ACCESS, PROMPT, or ALLOW mode.
"""

from enum import IntEnum


class PermissionMode(IntEnum):
    """Ordered permission levels — higher values grant broader access."""

    READ_ONLY = 10
    WORKSPACE_WRITE = 20
    DANGER_FULL_ACCESS = 30
    PROMPT = 40
    ALLOW = 50
