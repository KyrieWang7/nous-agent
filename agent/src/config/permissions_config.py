"""Configuration for the layered permission model."""

from __future__ import annotations

import logging
from typing import Any

from pydantic import BaseModel, Field

logger = logging.getLogger(__name__)

_permissions_config: PermissionsConfig | None = None


class PermissionsConfig(BaseModel):
    """Permissions section of config.yaml.

    ``mode`` maps to :class:`PermissionMode`:
      - ``"allow"`` (default) — all tools are permitted (backward-compatible)
      - ``"prompt"`` — unknown/high-risk tools require interactive approval
      - ``"workspace_write"`` — only read + workspace-write tools allowed
      - ``"read_only"`` — strictly read-only tools
      - ``"danger_full_access"`` — everything except auto-allow
    """

    enabled: bool = Field(default=True, description="Master switch for permission checks")
    mode: str = Field(default="allow", description="Session permission mode")
    tool_overrides: dict[str, str] = Field(
        default_factory=dict,
        description="Per-tool permission level overrides (tool_name -> mode string)",
    )


def load_permissions_config_from_dict(data: dict[str, Any] | None) -> None:
    global _permissions_config
    if data is None:
        _permissions_config = PermissionsConfig()
    else:
        _permissions_config = PermissionsConfig.model_validate(data)


def get_permissions_config() -> PermissionsConfig:
    global _permissions_config
    if _permissions_config is None:
        _permissions_config = PermissionsConfig()
    return _permissions_config
