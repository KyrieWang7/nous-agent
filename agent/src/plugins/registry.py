"""Plugin registry — aggregates tools and hooks from all enabled plugins.

Ensures global tool name uniqueness across plugins and between plugin tools
and builtin tools.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

from src.hooks.types import HookConfig
from src.plugins.manifest import PluginManifest, PluginToolDef

logger = logging.getLogger(__name__)


@dataclass
class PluginRegistry:
    """Collect and validate contributions from multiple plugins."""

    _plugins: list[PluginManifest] = field(default_factory=list)
    _tool_owners: dict[str, str] = field(default_factory=dict)

    def register(self, manifest: PluginManifest, *, builtin_tool_names: set[str] | None = None) -> list[str]:
        """Register a plugin. Returns a list of validation errors (empty = success)."""
        errors: list[str] = []
        builtins = builtin_tool_names or set()

        for tool in manifest.tools:
            if tool.name in builtins:
                errors.append(f"Plugin '{manifest.name}' tool '{tool.name}' conflicts with a builtin tool")
            elif tool.name in self._tool_owners:
                errors.append(f"Plugin '{manifest.name}' tool '{tool.name}' conflicts with plugin '{self._tool_owners[tool.name]}'")
            else:
                self._tool_owners[tool.name] = manifest.name

        if errors:
            logger.error("Plugin '%s' registration failed: %s", manifest.name, "; ".join(errors))
            return errors

        self._plugins.append(manifest)
        logger.info("Registered plugin '%s' (v%s) — %d tools, hooks=%s", manifest.name, manifest.version, len(manifest.tools), bool(manifest.hooks.pre_tool_use or manifest.hooks.post_tool_use))
        return []

    @property
    def plugins(self) -> list[PluginManifest]:
        return [p for p in self._plugins if p.enabled]

    def aggregated_tools(self) -> list[PluginToolDef]:
        """All tools from enabled plugins (already validated for uniqueness)."""
        tools: list[PluginToolDef] = []
        for plugin in self.plugins:
            tools.extend(plugin.tools)
        return tools

    def aggregated_hook_configs(self) -> list[HookConfig]:
        """Merge hook entries from all enabled plugins into HookConfig instances."""
        configs: list[HookConfig] = []
        for plugin in self.plugins:
            root = str(plugin.root_path)
            for cmd in plugin.hooks.pre_tool_use:
                full_cmd = cmd if cmd.startswith("/") else f"{root}/{cmd}"
                configs.append(HookConfig(command=full_cmd, events=["pre_tool_use"]))
            for cmd in plugin.hooks.post_tool_use:
                full_cmd = cmd if cmd.startswith("/") else f"{root}/{cmd}"
                configs.append(HookConfig(command=full_cmd, events=["post_tool_use"]))
            for cmd in plugin.hooks.post_tool_use_failure:
                full_cmd = cmd if cmd.startswith("/") else f"{root}/{cmd}"
                configs.append(HookConfig(command=full_cmd, events=["post_tool_use_failure"]))
        return configs

    def tool_permission_specs(self) -> list[tuple[str, str]]:
        """Return (tool_name, permission_mode_str) pairs for all plugin tools."""
        return [(t.name, t.required_permission) for t in self.aggregated_tools()]
