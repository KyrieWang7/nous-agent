"""DeerFlow Sandbox - Code execution environment abstraction.

Production-grade sandbox with:
- Thread-safe LRU caching for high-concurrency scenarios
- Path traversal protection (prevents .. escape attacks)
- Read-only mount enforcement (EROFS on write to protected paths)
- Agent-written path tracking for safe reverse resolution
"""

from src.exceptions import (
    SandboxCommandError,
    SandboxError,
    SandboxFileError,
    SandboxNotFoundError,
    SandboxPathTraversalError,
    SandboxPermissionError,
    SandboxReadOnlyError,
)
from src.local_sandbox import LocalSandbox
from src.path_mapping import PathMapping
from src.providers.local_provider import LocalSandboxProvider
from src.sandbox import Sandbox

__all__ = [
    "LocalSandbox",
    "LocalSandboxProvider",
    "PathMapping",
    "Sandbox",
    "SandboxCommandError",
    "SandboxError",
    "SandboxFileError",
    "SandboxNotFoundError",
    "SandboxPathTraversalError",
    "SandboxPermissionError",
    "SandboxReadOnlyError",
]
