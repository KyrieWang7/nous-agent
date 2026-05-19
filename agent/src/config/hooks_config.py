"""Configuration for the hook governance layer."""

from __future__ import annotations

import logging
from typing import Any

from pydantic import BaseModel, Field

logger = logging.getLogger(__name__)

_hooks_config: HooksConfig | None = None


class HookEntryConfig(BaseModel):
    """A single hook entry — either an external command or a Python callable."""

    command: str | None = Field(default=None, description="Shell command to run as hook")
    use: str | None = Field(default=None, description="Python module:attr path for hook callable")
    tools: list[str] | None = Field(default=None, description="Restrict to these tool names (None = all)")


class HooksConfig(BaseModel):
    """Hooks section of config.yaml."""

    enabled: bool = Field(default=True, description="Master switch for hooks")
    pre_tool_use: list[HookEntryConfig] = Field(default_factory=list)
    post_tool_use: list[HookEntryConfig] = Field(default_factory=list)
    post_tool_use_failure: list[HookEntryConfig] = Field(default_factory=list)


def load_hooks_config_from_dict(data: dict[str, Any] | None) -> None:
    global _hooks_config
    if data is None:
        _hooks_config = HooksConfig()
    else:
        _hooks_config = HooksConfig.model_validate(data)


def get_hooks_config() -> HooksConfig:
    global _hooks_config
    if _hooks_config is None:
        _hooks_config = HooksConfig()
    return _hooks_config
