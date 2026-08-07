"""Thread-safe local sandbox provider with LRU caching.

Designed for high-concurrency gateway scenarios where multiple FastAPI workers
concurrently acquire sandbox instances for different user sessions/threads.
"""

import logging
import threading
from collections import OrderedDict
from pathlib import Path

from src.config.paths import (
    ACP_VIRTUAL_PATH_PREFIX,
    VIRTUAL_PATH_PREFIX,
    get_paths,
)
from src.sandbox.local.local_sandbox import LocalSandbox, PathMapping
from src.sandbox.sandbox import Sandbox
from src.sandbox.sandbox_provider import SandboxProvider

logger = logging.getLogger(__name__)

# Module-level singleton kept for backward compatibility with code that
# references _singleton directly.
_singleton: LocalSandbox | None = None

DEFAULT_MAX_CACHED_THREAD_SANDBOXES = 256


class LocalSandboxProvider(SandboxProvider):
    """Thread-safe local sandbox provider with per-thread isolation and LRU eviction.

    Thread-safety: acquire, get, release and reset may be invoked from
    multiple threads (FastAPI workers, background tasks, subagent pools)
    so all cache mutations are serialized through a provider-wide Lock.

    Memory bound: _thread_sandboxes is an LRU cache (OrderedDict) capped at
    max_cached_threads. When exceeded, the least-recently-used entry is evicted.
    Eviction only loses _agent_written_paths tracking — graceful degradation.

    Isolation: Each thread_id gets its own LocalSandbox with per-thread path
    mappings, preventing cross-session data leakage.
    """

    def __init__(self, max_cached_threads: int = DEFAULT_MAX_CACHED_THREAD_SANDBOXES):
        """Initialize the local sandbox provider with path mappings.

        Args:
            max_cached_threads: Upper bound on per-thread sandboxes retained
                in the LRU cache.
        """
        self._path_mappings = self._setup_path_mappings()
        self._generic_sandbox: LocalSandbox | None = None
        self._thread_sandboxes: OrderedDict[str, LocalSandbox] = OrderedDict()
        self._max_cached_threads = max_cached_threads
        self._lock = threading.Lock()

    def _setup_path_mappings(self) -> list[PathMapping]:
        """Setup static path mappings shared by every sandbox this provider yields.

        Returns:
            List of static path mappings (e.g., skills directory).
        """
        mappings: list[PathMapping] = []

        try:
            from src.config import get_app_config

            config = get_app_config()
            skills_path = config.skills.get_skills_path()
            container_path = config.skills.container_path

            if skills_path.exists():
                mappings.append(
                    PathMapping(
                        container_path=container_path,
                        local_path=str(skills_path),
                        read_only=True,  # Skills directory is always read-only
                    )
                )

            # Map custom mounts from sandbox config if available
            sandbox_config = getattr(config, "sandbox", None)
            if sandbox_config and hasattr(sandbox_config, "mounts") and sandbox_config.mounts:
                for mount in sandbox_config.mounts:
                    host_path = Path(mount.host_path)
                    mount_container_path = mount.container_path.rstrip("/") or "/"

                    if not host_path.is_absolute():
                        logger.warning(
                            "Mount host_path must be absolute, skipping: %s -> %s",
                            mount.host_path,
                            mount.container_path,
                        )
                        continue

                    if host_path.exists():
                        mappings.append(
                            PathMapping(
                                container_path=mount_container_path,
                                local_path=str(host_path.resolve()),
                                read_only=getattr(mount, "read_only", False),
                            )
                        )
                    else:
                        logger.warning(
                            "Mount host_path does not exist, skipping: %s -> %s",
                            mount.host_path,
                            mount.container_path,
                        )
        except Exception as e:
            logger.warning("Could not setup path mappings: %s", e, exc_info=True)

        return mappings

    def acquire(self, thread_id: str | None = None) -> str:
        """Acquire or retrieve a cached sandbox for the given thread.

        Thread-safe: concurrent calls for the same thread_id observe the
        same sandbox instance. Accessing an existing sandbox promotes it
        in the LRU eviction queue.

        Args:
            thread_id: Session/thread identifier. If None, returns the
                generic singleton with id "local".

        Returns:
            The sandbox_id.
        """
        global _singleton

        if thread_id is None:
            with self._lock:
                if self._generic_sandbox is None:
                    self._generic_sandbox = LocalSandbox(
                        "local", path_mappings=list(self._path_mappings)
                    )
                    _singleton = self._generic_sandbox
                return self._generic_sandbox.id

        with self._lock:
            cached = self._thread_sandboxes.get(thread_id)
            if cached is not None:
                self._thread_sandboxes.move_to_end(thread_id)
                return cached.id

            # Build per-thread sandbox with base mappings + per-thread user-data
            # mappings so that agent writes to /mnt/user-data/... land under
            # {base_dir}/threads/{thread_id}/user-data/... on the host. Without
            # this the gateway artifact endpoint cannot find the file because
            # the agent ends up writing to the literal container path.
            paths = get_paths()
            try:
                paths.ensure_thread_dirs(thread_id)
            except ValueError as e:
                # Defensive: thread_id may not match the safe regex. In that
                # case fall back to the static mappings only.
                logger.warning("ensure_thread_dirs(%s) failed: %s", thread_id, e)
                per_thread_mappings: list[PathMapping] = []
            else:
                per_thread_mappings = [
                    PathMapping(
                        container_path=VIRTUAL_PATH_PREFIX,
                        local_path=str(paths.sandbox_user_data_dir(thread_id)),
                        read_only=False,
                    ),
                    PathMapping(
                        container_path=ACP_VIRTUAL_PATH_PREFIX,
                        local_path=str(paths.acp_workspace_dir(thread_id)),
                        read_only=False,
                    ),
                ]

            sandbox = LocalSandbox(
                f"local:{thread_id}",
                path_mappings=list(self._path_mappings) + per_thread_mappings,
            )
            self._thread_sandboxes[thread_id] = sandbox
            self._evict_if_over_capacity()
            return sandbox.id

    def get(self, sandbox_id: str) -> Sandbox | None:
        """Get a sandbox by ID, promoting it in the LRU order.

        Args:
            sandbox_id: The sandbox identifier to look up.

        Returns:
            The sandbox instance, or None if not found.
        """
        if sandbox_id == "local":
            with self._lock:
                if self._generic_sandbox is None:
                    self.acquire()
                return self._generic_sandbox

        if isinstance(sandbox_id, str) and sandbox_id.startswith("local:"):
            thread_id = sandbox_id[len("local:"):]
            with self._lock:
                cached = self._thread_sandboxes.get(thread_id)
                if cached is not None:
                    self._thread_sandboxes.move_to_end(thread_id)
                return cached
        return None

    def release(self, sandbox_id: str) -> None:
        """Release a sandbox (no-op in LRU mode).

        We intentionally do NOT destroy the sandbox on release to preserve
        _agent_written_paths state across multiple turns. Cleanup happens via
        LRU eviction or explicit reset()/shutdown().
        """
        # No-op: LRU eviction handles cleanup
        pass

    def force_release(self, sandbox_id: str) -> None:
        """Forcefully remove a sandbox from cache.

        Use for explicit session termination (user logout, etc.).

        Args:
            sandbox_id: The sandbox identifier to remove.
        """
        if sandbox_id.startswith("local:"):
            thread_id = sandbox_id[len("local:"):]
            with self._lock:
                self._thread_sandboxes.pop(thread_id, None)

    def reset(self) -> None:
        """Drop all cached sandbox instances.

        Called when configuration/mount changes need to take effect.
        """
        global _singleton
        with self._lock:
            self._generic_sandbox = None
            self._thread_sandboxes.clear()
            _singleton = None

    def shutdown(self) -> None:
        """Shutdown the provider, releasing all resources."""
        self.reset()

    @property
    def cache_size(self) -> int:
        """Current number of cached thread sandbox instances (for monitoring)."""
        with self._lock:
            return len(self._thread_sandboxes)

    def _evict_if_over_capacity(self) -> None:
        """LRU-evict cached thread sandboxes once the cap is exceeded.

        Caller MUST hold self._lock.
        """
        while len(self._thread_sandboxes) > self._max_cached_threads:
            evicted_id, _ = self._thread_sandboxes.popitem(last=False)
            logger.info(
                "LRU eviction: thread sandbox %s removed (cap=%d)",
                evicted_id,
                self._max_cached_threads,
            )
