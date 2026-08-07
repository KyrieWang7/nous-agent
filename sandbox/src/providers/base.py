"""Sandbox provider abstraction and implementations."""

from abc import ABC, abstractmethod

from src.sandbox import Sandbox


class SandboxProvider(ABC):
    """Abstract base class for sandbox providers.

    Providers manage the lifecycle of sandbox instances, handling allocation,
    retrieval, release, and cleanup. Implementations must be thread-safe for
    use in concurrent gateway environments.
    """

    @abstractmethod
    def acquire(self, thread_id: str | None = None) -> str:
        """Acquire a sandbox environment and return its ID.

        Args:
            thread_id: Optional session/thread identifier. If None, the
                provider generates a unique ID.

        Returns:
            The sandbox identifier.
        """
        pass

    @abstractmethod
    def get(self, sandbox_id: str) -> Sandbox | None:
        """Get a sandbox environment by ID.

        Args:
            sandbox_id: The identifier of the sandbox to retrieve.

        Returns:
            The sandbox instance, or None if not found.
        """
        pass

    @abstractmethod
    def release(self, sandbox_id: str) -> None:
        """Release a sandbox environment.

        Semantics are implementation-defined: may destroy immediately,
        or mark for lazy eviction (e.g., LRU).

        Args:
            sandbox_id: The identifier of the sandbox to release.
        """
        pass

    def reset(self) -> None:
        """Clear cached state so configuration changes take effect.

        Called by provider management code when mounts or config change.
        Implementations should drop all cached instances.
        """
        pass

    def shutdown(self) -> None:
        """Shutdown the provider, releasing all resources.

        Called during application shutdown for clean teardown.
        """
        pass
