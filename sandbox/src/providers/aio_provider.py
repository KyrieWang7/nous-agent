"""AIO sandbox provider - manages sandbox via external AIO service."""

import uuid

from src.aio_sandbox import AioSandbox
from src.providers.base import SandboxProvider
from src.sandbox import Sandbox


class AioSandboxProvider(SandboxProvider):
    """Provider that manages sandbox instances via external AIO service."""

    def __init__(
        self,
        base_url: str = "http://localhost:8080",
        auto_start: bool = True,
        **kwargs,
    ):
        self._base_url = base_url
        self._auto_start = auto_start
        self._sandboxes: dict[str, AioSandbox] = {}

    def acquire(self, thread_id: str | None = None) -> str:
        sandbox_id = thread_id or str(uuid.uuid4())
        if sandbox_id not in self._sandboxes:
            self._sandboxes[sandbox_id] = AioSandbox(
                id=sandbox_id,
                base_url=self._base_url,
            )
        return sandbox_id

    def get(self, sandbox_id: str) -> Sandbox | None:
        return self._sandboxes.get(sandbox_id)

    def release(self, sandbox_id: str) -> None:
        sandbox = self._sandboxes.pop(sandbox_id, None)
        if sandbox:
            sandbox.close()

    def shutdown(self) -> None:
        for sandbox in self._sandboxes.values():
            sandbox.close()
        self._sandboxes.clear()
