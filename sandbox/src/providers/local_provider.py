"""Local sandbox provider - manages local sandbox instances."""

import uuid

from src.local_sandbox import LocalSandbox
from src.providers.base import SandboxProvider
from src.sandbox import Sandbox


class LocalSandboxProvider(SandboxProvider):
    """Provides local sandbox instances for development."""

    def __init__(self, path_mappings: dict[str, str] | None = None, **kwargs):
        self._sandboxes: dict[str, LocalSandbox] = {}
        self._path_mappings = path_mappings or {}

    def acquire(self, thread_id: str | None = None) -> str:
        sandbox_id = thread_id or str(uuid.uuid4())
        if sandbox_id not in self._sandboxes:
            self._sandboxes[sandbox_id] = LocalSandbox(
                id=sandbox_id,
                path_mappings=self._path_mappings,
            )
        return sandbox_id

    def get(self, sandbox_id: str) -> Sandbox | None:
        return self._sandboxes.get(sandbox_id)

    def release(self, sandbox_id: str) -> None:
        self._sandboxes.pop(sandbox_id, None)

    def shutdown(self) -> None:
        self._sandboxes.clear()
