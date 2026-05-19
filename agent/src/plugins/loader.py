"""Plugin discovery and loading.

Scans configured directories for ``plugin.json`` manifests and builds a
:class:`PluginRegistry`.
"""

from __future__ import annotations

import logging
from pathlib import Path

from src.plugins.manifest import PluginManifest
from src.plugins.registry import PluginRegistry

logger = logging.getLogger(__name__)


def discover_plugins(
    directories: list[str | Path],
    *,
    builtin_tool_names: set[str] | None = None,
) -> PluginRegistry:
    """Scan *directories* for plugin.json files and return a registry.

    Each immediate subdirectory that contains a ``plugin.json`` is treated
    as a plugin.  Plugins with validation errors are logged and skipped.
    """
    registry = PluginRegistry()

    for dir_path in directories:
        base = Path(dir_path).expanduser()
        if not base.is_dir():
            logger.debug("Plugin directory does not exist: %s", base)
            continue

        for child in sorted(base.iterdir()):
            manifest_path = child / "plugin.json"
            if not manifest_path.is_file():
                continue
            try:
                manifest = PluginManifest.from_file(manifest_path)
            except Exception:
                logger.exception("Failed to load plugin manifest: %s", manifest_path)
                continue

            errors = registry.register(manifest, builtin_tool_names=builtin_tool_names)
            if errors:
                logger.warning("Skipping plugin '%s' due to errors", manifest.name)

    return registry
