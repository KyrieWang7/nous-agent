"""Local container backend for sandbox provisioning.

Manages sandbox containers using Docker Python SDK via docker.sock.
Works both on the host and inside a Docker container (when docker.sock is mounted).
"""

from __future__ import annotations

import logging
import os
import re

import docker
from docker.errors import APIError, NotFound
from docker.models.containers import Container

from .backend import SandboxBackend, wait_for_sandbox_ready
from .sandbox_info import SandboxInfo

logger = logging.getLogger(__name__)

_SENSITIVE_ENV = re.compile(r"(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|DATABASE|REDIS|CREDENTIAL)", re.IGNORECASE)


class LocalContainerBackend(SandboxBackend):
    """Backend that manages per-thread sandbox containers via Docker SDK.

    Each thread gets an isolated sandbox container with its own /mnt/user-data
    volume mounts. Containers are connected to the same Docker network as the
    caller so they can be reached by container name.

    Requires /var/run/docker.sock to be mounted when running inside a container.
    """

    def __init__(
        self,
        *,
        image: str,
        base_port: int,
        container_prefix: str,
        config_mounts: list,
        environment: dict[str, str],
    ):
        self._image = image
        self._base_port = base_port
        self._container_prefix = container_prefix
        self._config_mounts = config_mounts
        self._environment = environment
        self._docker = docker.from_env()
        self._network = os.getenv("SANDBOX_DOCKER_NETWORK", "docker_nous-ai-network")

    # ── SandboxBackend interface ──────────────────────────────────────────

    def create(
        self,
        thread_id: str,
        sandbox_id: str,
        extra_mounts: list[tuple[str, str, bool]] | None = None,
    ) -> SandboxInfo:
        container_name = f"{self._container_prefix}-{sandbox_id}"

        self._remove_if_exists(container_name)

        volumes = self._build_volumes(extra_mounts)
        # Never copy agent/gateway credentials into model-controlled workloads.
        # Sandbox-specific non-secret settings can still be supplied explicitly.
        environment = {
            key: value for key, value in self._environment.items()
            if not _SENSITIVE_ENV.search(key)
        }

        try:
            container: Container = self._docker.containers.run(
                image=self._image,
                name=container_name,
                detach=True,
                remove=True,
                network=self._network,
                environment=environment,
                volumes=volumes,
                user="gem",
                read_only=True,
                tmpfs={
                    "/tmp": "rw,noexec,nosuid,nodev,size=64m",
                    "/run": "rw,noexec,nosuid,nodev,size=8m",
                },
                cap_drop=["ALL"],
                security_opt=["no-new-privileges"],
                mem_limit="512m",
                nano_cpus=1_000_000_000,
                pids_limit=128,
                ulimits=[docker.types.Ulimit(name="nofile", soft=1024, hard=1024)],
                labels={"io.nous-agent.sandbox": "true", "io.nous-agent.managed-by": "aio-provisioner"},
            )
        except APIError as e:
            raise RuntimeError(f"Failed to start sandbox container: {e}")

        self._init_user_data_dirs(container)

        sandbox_url = f"http://{container_name}:8080"

        logger.info(
            f"Started sandbox container {container_name} "
            f"(id={container.short_id}) on network {self._network}"
        )

        return SandboxInfo(
            sandbox_id=sandbox_id,
            sandbox_url=sandbox_url,
            container_name=container_name,
            container_id=container.id,
        )

    def destroy(self, info: SandboxInfo) -> None:
        try:
            container = self._docker.containers.get(info.container_id or info.container_name)
            container.stop(timeout=5)
            logger.info(f"Stopped sandbox container {info.container_name}")
        except NotFound:
            logger.debug(f"Container {info.container_name} already removed")
        except APIError as e:
            logger.warning(f"Failed to stop container {info.container_name}: {e}")

    def is_alive(self, info: SandboxInfo) -> bool:
        try:
            container = self._docker.containers.get(info.container_name)
            return container.status == "running"
        except (NotFound, APIError):
            return False

    def discover(self, sandbox_id: str) -> SandboxInfo | None:
        container_name = f"{self._container_prefix}-{sandbox_id}"
        try:
            container = self._docker.containers.get(container_name)
            if container.status != "running":
                return None
        except (NotFound, APIError):
            return None

        sandbox_url = f"http://{container_name}:8080"
        if not wait_for_sandbox_ready(sandbox_url, timeout=5):
            return None

        return SandboxInfo(
            sandbox_id=sandbox_id,
            sandbox_url=sandbox_url,
            container_name=container_name,
            container_id=container.id,
        )

    # ── Helpers ───────────────────────────────────────────────────────────

    def _build_volumes(
        self, extra_mounts: list[tuple[str, str, bool]] | None
    ) -> dict[str, dict[str, str]]:
        """Build Docker SDK volumes dict from config + extra mounts."""
        volumes: dict[str, dict[str, str]] = {}

        for mount in self._config_mounts:
            volumes[mount.host_path] = {
                "bind": mount.container_path,
                "mode": "ro" if mount.read_only else "rw",
            }

        if extra_mounts:
            for host_path, container_path, read_only in extra_mounts:
                volumes[host_path] = {
                    "bind": container_path,
                    "mode": "ro" if read_only else "rw",
                }

        return volumes

    @staticmethod
    def _init_user_data_dirs(container: Container) -> None:
        """Create /mnt/user-data directories inside the sandbox container.

        The sandbox image runs its service as the `gem` user, but /mnt is
        owned by root. We need to create the workspace/uploads/outputs dirs
        and set ownership so the sandbox API can write to them.
        """
        try:
            exit_code, output = container.exec_run(
                cmd=[
                    "sh", "-c",
                    "mkdir -p /mnt/user-data/workspace /mnt/user-data/uploads /mnt/user-data/outputs "
                    "&& chown -R gem:gem /mnt/user-data "
                    "&& chmod -R u+rwX,g+rwX,o-rwx /mnt/user-data",
                ],
                user="root",
            )
            if exit_code != 0:
                logger.warning(
                    f"Failed to init /mnt/user-data dirs (exit={exit_code}): "
                    f"{output.decode() if output else ''}"
                )
        except APIError as e:
            logger.warning(f"Failed to exec init command in sandbox: {e}")

    def _remove_if_exists(self, container_name: str) -> None:
        """Remove a container if it already exists (stale from previous run)."""
        try:
            old = self._docker.containers.get(container_name)
            old.remove(force=True)
            logger.info(f"Removed stale container {container_name}")
        except NotFound:
            pass
        except APIError as e:
            logger.warning(f"Failed to remove stale container {container_name}: {e}")
