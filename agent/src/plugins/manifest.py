"""Plugin manifest model — declarative plugin.json schema.

A plugin manifest declares what a plugin contributes to the runtime:
tools, hooks, permissions, lifecycle commands, and metadata.
"""

from __future__ import annotations

import json
import logging
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)


@dataclass
class PluginToolDef:
    """A tool contributed by a plugin."""

    name: str
    description: str
    command: str
    input_schema: dict[str, Any] = field(default_factory=dict)
    required_permission: str = "danger_full_access"


@dataclass
class PluginHooksDef:
    """Hook commands contributed by a plugin."""

    pre_tool_use: list[str] = field(default_factory=list)
    post_tool_use: list[str] = field(default_factory=list)
    post_tool_use_failure: list[str] = field(default_factory=list)


@dataclass
class PluginPermissions:
    """Capability declarations for the plugin."""

    read: bool = True
    write: bool = False
    execute: bool = False


@dataclass
class PluginManifest:
    """Parsed and validated plugin.json."""

    name: str
    version: str = "0.0.0"
    description: str = ""
    root_path: Path = field(default_factory=lambda: Path("."))
    enabled: bool = True
    tools: list[PluginToolDef] = field(default_factory=list)
    hooks: PluginHooksDef = field(default_factory=PluginHooksDef)
    permissions: PluginPermissions = field(default_factory=PluginPermissions)

    @classmethod
    def from_file(cls, path: Path) -> PluginManifest:
        """Load and validate a plugin.json file."""
        with open(path, encoding="utf-8") as f:
            raw = json.load(f)
        return cls.from_dict(raw, root_path=path.parent)

    @classmethod
    def from_dict(cls, data: dict[str, Any], root_path: Path | None = None) -> PluginManifest:
        errors: list[str] = []
        name = data.get("name")
        if not name or not isinstance(name, str):
            errors.append("'name' is required and must be a string")
            name = "<invalid>"

        tools = []
        for i, t in enumerate(data.get("tools", [])):
            if not t.get("name"):
                errors.append(f"tools[{i}].name is required")
                continue
            if not t.get("command"):
                errors.append(f"tools[{i}].command is required")
                continue
            tools.append(
                PluginToolDef(
                    name=t["name"],
                    description=t.get("description", ""),
                    command=t["command"],
                    input_schema=t.get("inputSchema", t.get("input_schema", {})),
                    required_permission=t.get("requiredPermission", t.get("required_permission", "danger_full_access")),
                )
            )

        hooks_raw = data.get("hooks", {})
        hooks = PluginHooksDef(
            pre_tool_use=hooks_raw.get("pre_tool_use", []),
            post_tool_use=hooks_raw.get("post_tool_use", []),
            post_tool_use_failure=hooks_raw.get("post_tool_use_failure", []),
        )

        perms_raw = data.get("permissions", {})
        permissions = PluginPermissions(
            read=perms_raw.get("read", True),
            write=perms_raw.get("write", False),
            execute=perms_raw.get("execute", False),
        )

        if errors:
            logger.warning("Plugin '%s' manifest has validation errors: %s", name, "; ".join(errors))

        return cls(
            name=name,
            version=data.get("version", "0.0.0"),
            description=data.get("description", ""),
            root_path=root_path or Path("."),
            tools=tools,
            hooks=hooks,
            permissions=permissions,
        )
