"""Static sandbox backend — connects to a pre-existing sandbox at a fixed URL.

Used when a standalone sandbox container is managed externally (e.g., via
docker-compose) and does not need lifecycle management from the backend.

Typical config.yaml::

    sandbox:
      use: src.community.aio_sandbox:AioSandboxProvider
      base_url: http://sandbox:8080
      auto_start: false
"""

from __future__ import annotations

import logging

from .backend import SandboxBackend, wait_for_sandbox_ready
from .sandbox_info import SandboxInfo

logger = logging.getLogger(__name__)


class StaticSandboxBackend(SandboxBackend):
    """Backend that connects to a pre-existing sandbox at a fixed URL.

    No containers are created or destroyed — the sandbox is assumed to be
    externally managed (e.g., a sibling service in docker-compose).
    """

    def __init__(self, base_url: str):
        self._base_url = base_url.rstrip("/")

    def create(
        self,
        thread_id: str,
        sandbox_id: str,
        extra_mounts: list[tuple[str, str, bool]] | None = None,
    ) -> SandboxInfo:
        if extra_mounts:
            logger.debug(
                "StaticSandboxBackend ignores extra_mounts — "
                "ensure volumes are configured in docker-compose"
            )
        return SandboxInfo(
            sandbox_id=sandbox_id,
            sandbox_url=self._base_url,
        )

    def destroy(self, info: SandboxInfo) -> None:
        logger.debug(f"StaticSandboxBackend.destroy({info.sandbox_id}) — no-op")

    def is_alive(self, info: SandboxInfo) -> bool:
        return wait_for_sandbox_ready(self._base_url, timeout=5)

    def discover(self, sandbox_id: str) -> SandboxInfo | None:
        if wait_for_sandbox_ready(self._base_url, timeout=5):
            return SandboxInfo(
                sandbox_id=sandbox_id,
                sandbox_url=self._base_url,
            )
        return None
