"""External process hook executor.

Protocol (compatible with Claude Code's hook convention):
  - stdin: JSON payload with event details
  - env:   HOOK_EVENT, HOOK_TOOL_NAME, HOOK_TOOL_INPUT
  - exit 0  -> allow  (stdout becomes optional message)
  - exit 2  -> deny   (stdout becomes denial reason)
  - other   -> warn   (non-blocking, logged)
"""

from __future__ import annotations

import json
import logging
import os
import subprocess
from typing import Any

from src.hooks.types import HookPayload, HookResult

logger = logging.getLogger(__name__)

_HOOK_TIMEOUT_SECONDS = 30

_project_root: str | None = None


def _get_project_root() -> str:
    """Return the project root directory (where config.yaml lives).

    Hook commands use relative paths that should resolve against the project
    root, not the subprocess's inherited cwd (which may differ when
    LangGraph is started from a subdirectory like ``backend/``).
    """
    global _project_root
    if _project_root is not None:
        return _project_root

    try:
        from src.config.app_config import AppConfig

        config_path = AppConfig.resolve_config_path()
        _project_root = str(config_path.parent)
    except Exception:
        _project_root = os.getcwd()
    return _project_root


def run_external_hook(
    command: str,
    payload: HookPayload,
    *,
    timeout: int = _HOOK_TIMEOUT_SECONDS,
) -> HookResult:
    """Execute *command* as a subprocess following the external hook protocol."""
    env = {
        **os.environ,
        "HOOK_EVENT": payload.event,
        "HOOK_TOOL_NAME": payload.tool_name,
    }
    if payload.tool_input is not None:
        env["HOOK_TOOL_INPUT"] = json.dumps(payload.tool_input, default=str)

    stdin_bytes = json.dumps(_payload_to_dict(payload), default=str).encode()

    try:
        proc = subprocess.run(
            command,
            input=stdin_bytes,
            capture_output=True,
            timeout=timeout,
            shell=True,
            env=env,
            cwd=_get_project_root(),
        )
    except subprocess.TimeoutExpired:
        logger.warning("Hook timed out after %ds: %s", timeout, command)
        return HookResult.warned(f"Hook timed out after {timeout}s")
    except FileNotFoundError:
        logger.error("Hook command not found: %s", command)
        return HookResult.warned(f"Hook command not found: {command}")
    except Exception as exc:
        logger.exception("Hook execution error: %s", command)
        return HookResult.warned(f"Hook error: {exc}")

    stdout = proc.stdout.decode(errors="replace").strip()

    if proc.returncode == 0:
        return HookResult.allowed(message=stdout or None)
    elif proc.returncode == 2:
        return HookResult.denied(message=stdout or "denied by hook")
    else:
        logger.warning(
            "Hook exited with code %d (warn): %s — %s",
            proc.returncode,
            command,
            stdout[:200],
        )
        return HookResult.warned(message=stdout or f"hook exited with code {proc.returncode}")


def _payload_to_dict(p: HookPayload) -> dict[str, Any]:
    d: dict[str, Any] = {"event": p.event, "tool_name": p.tool_name}
    if p.tool_input is not None:
        d["input"] = p.tool_input
    if p.tool_output is not None:
        d["output"] = p.tool_output
    d["is_error"] = p.is_error
    return d
