"""Sandbox provider abstraction and implementations."""

from abc import ABC, abstractmethod

from src.sandbox import Sandbox


class SandboxProvider(ABC):
    """Abstract base class for sandbox providers."""

    @abstractmethod
    def acquire(self, thread_id: str | None = None) -> str:
        """Acquire a sandbox environment and return its ID."""
        pass

    @abstractmethod
    def get(self, sandbox_id: str) -> Sandbox | None:
        """Get a sandbox environment by ID."""
        pass

    @abstractmethod
    def release(self, sandbox_id: str) -> None:
        """Release a sandbox environment."""
        pass

    def shutdown(self) -> None:
        """Shutdown the provider (optional)."""
        pass
