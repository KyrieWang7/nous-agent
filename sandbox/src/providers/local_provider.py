"""Local sandbox provider with thread-safe LRU caching for production workloads.

Designed for high-concurrency gateway scenarios where multiple threads/workers
concurrently acquire sandbox instances for different user sessions.

Key features:
- Thread-safe cache operations via threading.Lock
- LRU eviction to bound memory in long-running processes
- Per-thread sandbox isolation with session-scoped path mappings
- Graceful degradation on eviction (only loses _agent_written_paths tracking)
"""

import logging
import threading
import uuid
from collections import OrderedDict

from src.local_sandbox import LocalSandbox
from src.path_mapping import PathMapping
from src.providers.base import SandboxProvider
from src.sandbox import Sandbox

logger = logging.getLogger(__name__)

# Default upper bound on per-thread LocalSandbox instances retained in memory.
# Each cached instance is a small Python object, but in a long-running gateway
# the number of distinct thread_ids is unbounded. When the cap is exceeded the
# least-recently-used entry is dropped; the next acquire() for that thread
# simply rebuilds the sandbox at the cost of losing its _agent_written_paths.
DEFAULT_MAX_CACHED_SANDBOXES = 256


class LocalSandboxProvider(SandboxProvider):
    """Thread-safe local sandbox provider with LRU eviction.

    Thread-safety: acquire, get, release and reset may be invoked from
    multiple threads (FastAPI workers, background tasks, subagent pools)
    so all cache mutations are serialized through a provider-wide Lock.

    Memory bound: _sandboxes is an LRU cache (OrderedDict) capped at
    max_cached. When exceeded, the least-recently-used entry is evicted.
    The evicted thread's next acquire() rebuilds a fresh sandbox — the only
    cost is losing its _agent_written_paths reverse-resolve hints, which
    gracefully degrades read_file output to no reverse resolution.
    """

    def __init__(
        self,
        path_mappings: list[PathMapping] | None = None,
        max_cached: int = DEFAULT_MAX_CACHED_SANDBOXES,
        **kwargs,
    ):
        """Initialize the provider with path mappings and cache configuration.

        Args:
            path_mappings: Base path mappings applied to every sandbox instance.
                These represent static mounts (skills, shared resources).
            max_cached: Maximum number of sandbox instances to retain in the
                LRU cache. Oldest entries are evicted when exceeded.
        """
        self._base_mappings: list[PathMapping] = path_mappings or []
        self._sandboxes: OrderedDict[str, LocalSandbox] = OrderedDict()
        self._max_cached = max_cached
        self._lock = threading.Lock()

    def acquire(self, thread_id: str | None = None) -> str:
        """Acquire or retrieve a cached sandbox for the given thread.

        Thread-safe: concurrent calls for the same thread_id will observe
        the same sandbox instance (no duplicate creation). Concurrent calls
        for different thread_ids are serialized through the lock.

        LRU promotion: accessing an existing sandbox moves it to the end
        of the eviction queue, preventing active sessions from being evicted.

        Args:
            thread_id: Session/thread identifier. If None, a random UUID is
                generated (useful for one-off operations without session context).

        Returns:
            The sandbox_id (same as thread_id when provided).
        """
        sandbox_id = thread_id or str(uuid.uuid4())

        with self._lock:
            if sandbox_id in self._sandboxes:
                # Promote to most-recently-used
                self._sandboxes.move_to_end(sandbox_id)
                return sandbox_id

            # Create new sandbox with base mappings
            sandbox = LocalSandbox(
                id=sandbox_id,
                path_mappings=list(self._base_mappings),
            )
            self._sandboxes[sandbox_id] = sandbox

            # LRU eviction
            self._evict_if_over_capacity()

        return sandbox_id

    def get(self, sandbox_id: str) -> Sandbox | None:
        """Get a sandbox by ID, promoting it in the LRU order.

        This method is called on every tool invocation (read_file, write_file,
        execute_command), so promoting on get() ensures active sandboxes stay
        in cache even if acquire() isn't called frequently.

        Args:
            sandbox_id: The sandbox identifier to look up.

        Returns:
            The sandbox instance, or None if not found (evicted or never created).
        """
        with self._lock:
            sandbox = self._sandboxes.get(sandbox_id)
            if sandbox is not None:
                self._sandboxes.move_to_end(sandbox_id)
            return sandbox

    def release(self, sandbox_id: str) -> None:
        """Release a sandbox (no-op in LRU mode).

        In production, we intentionally do NOT destroy the sandbox on release.
        The _agent_written_paths state is valuable across multiple turns within
        the same thread. Cleanup is handled by:
        - LRU eviction when cache capacity is exceeded
        - Explicit reset() or shutdown() calls

        Args:
            sandbox_id: The sandbox identifier to release.
        """
        # No-op: let LRU eviction handle cleanup.
        # This preserves cross-turn state (_agent_written_paths) for active sessions.
        pass

    def force_release(self, sandbox_id: str) -> None:
        """Forcefully remove a sandbox from cache.

        Use this only when you explicitly need to destroy a session's state,
        e.g., on user logout or explicit session termination.

        Args:
            sandbox_id: The sandbox identifier to forcefully remove.
        """
        with self._lock:
            self._sandboxes.pop(sandbox_id, None)

    def reset(self) -> None:
        """Drop all cached sandbox instances.

        Use when configuration or mount changes need to take effect on
        the next acquire(). Existing sandbox_ids become invalid.
        """
        with self._lock:
            self._sandboxes.clear()

    def shutdown(self) -> None:
        """Shutdown the provider, releasing all resources.

        Should be called during application shutdown to ensure clean state.
        """
        self.reset()

    @property
    def cache_size(self) -> int:
        """Current number of cached sandbox instances (for monitoring)."""
        with self._lock:
            return len(self._sandboxes)

    def _evict_if_over_capacity(self) -> None:
        """Evict least-recently-used entries until within capacity.

        Caller MUST hold self._lock.
        """
        while len(self._sandboxes) > self._max_cached:
            evicted_id, _ = self._sandboxes.popitem(last=False)
            logger.info(
                "LRU eviction: sandbox %s removed (cap=%d, current=%d)",
                evicted_id,
                self._max_cached,
                len(self._sandboxes),
            )
