"""Configuration for the plugin system."""

from __future__ import annotations

import logging
from typing import Any

from pydantic import BaseModel, Field

logger = logging.getLogger(__name__)

_plugins_config: PluginsConfig | None = None


class PluginsConfig(BaseModel):
    """Plugins section of config.yaml."""

    enabled: bool = Field(default=True, description="Master switch for plugin loading")
    directories: list[str] = Field(
        default_factory=lambda: ["~/.deerflow/plugins", ".deerflow/plugins"],
        description="Directories to scan for plugin.json manifests",
    )


def load_plugins_config_from_dict(data: dict[str, Any] | None) -> None:
    global _plugins_config
    if data is None:
        _plugins_config = PluginsConfig()
    else:
        _plugins_config = PluginsConfig.model_validate(data)


def get_plugins_config() -> PluginsConfig:
    global _plugins_config
    if _plugins_config is None:
        _plugins_config = PluginsConfig()
    return _plugins_config
