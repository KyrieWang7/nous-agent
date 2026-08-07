"""Sandbox-related exceptions with structured error information.

Provides a hierarchy of exceptions for sandbox operations, enabling
consistent error handling across the application in high-concurrency
production scenarios.
"""

import errno


class SandboxError(Exception):
    """Base exception for all sandbox-related errors."""

    def __init__(self, message: str, details: dict | None = None):
        super().__init__(message)
        self.message = message
        self.details = details or {}

    def __str__(self) -> str:
        if self.details:
            detail_str = ", ".join(f"{k}={v}" for k, v in self.details.items())
            return f"{self.message} ({detail_str})"
        return self.message


class SandboxNotFoundError(SandboxError):
    """Raised when a sandbox cannot be found or is not available."""

    def __init__(self, message: str = "Sandbox not found", sandbox_id: str | None = None):
        details = {"sandbox_id": sandbox_id} if sandbox_id else None
        super().__init__(message, details)
        self.sandbox_id = sandbox_id


class SandboxCommandError(SandboxError):
    """Raised when a command execution fails in the sandbox."""

    def __init__(
        self, message: str, command: str | None = None, exit_code: int | None = None
    ):
        details: dict = {}
        if command:
            details["command"] = command[:100] + "..." if len(command) > 100 else command
        if exit_code is not None:
            details["exit_code"] = exit_code
        super().__init__(message, details)
        self.command = command
        self.exit_code = exit_code


class SandboxFileError(SandboxError):
    """Raised when a file operation fails in the sandbox."""

    def __init__(
        self, message: str, path: str | None = None, operation: str | None = None
    ):
        details: dict = {}
        if path:
            details["path"] = path
        if operation:
            details["operation"] = operation
        super().__init__(message, details)
        self.path = path
        self.operation = operation


class SandboxPermissionError(SandboxFileError):
    """Raised when a permission error occurs during file operations.

    Includes path traversal attempts and read-only mount violations.
    """

    def __init__(
        self,
        message: str = "Permission denied",
        path: str | None = None,
        operation: str | None = None,
        err_code: int = errno.EACCES,
    ):
        super().__init__(message, path, operation)
        self.err_code = err_code


class SandboxReadOnlyError(SandboxPermissionError):
    """Raised when attempting to write to a read-only mounted path."""

    def __init__(self, path: str | None = None, operation: str | None = None):
        super().__init__(
            message="Read-only file system",
            path=path,
            operation=operation,
            err_code=errno.EROFS,
        )


class SandboxPathTraversalError(SandboxPermissionError):
    """Raised when a path traversal attack is detected."""

    def __init__(self, path: str | None = None):
        super().__init__(
            message="Access denied: path escapes mounted directory",
            path=path,
            operation="resolve",
            err_code=errno.EACCES,
        )
