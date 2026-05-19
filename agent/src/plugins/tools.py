"""Adapter to turn plugin tool definitions into LangChain BaseTool instances."""

from __future__ import annotations

import json
import logging
import os
import subprocess
from typing import Any

from langchain_core.tools import BaseTool

from src.plugins.manifest import PluginToolDef

logger = logging.getLogger(__name__)

_TOOL_TIMEOUT_SECONDS = 120


class PluginTool(BaseTool):
    """A tool backed by a plugin's external command."""

    name: str
    description: str
    command: str
    plugin_name: str = ""
    plugin_root: str = ""

    def _run(self, **kwargs: Any) -> str:
        env = {
            **os.environ,
            "DEERFLOW_PLUGIN_NAME": self.plugin_name,
            "DEERFLOW_PLUGIN_ROOT": self.plugin_root,
            "DEERFLOW_TOOL_NAME": self.name,
        }
        stdin_data = json.dumps(kwargs, default=str).encode()

        try:
            proc = subprocess.run(
                self.command,
                input=stdin_data,
                capture_output=True,
                timeout=_TOOL_TIMEOUT_SECONDS,
                shell=True,
                env=env,
            )
        except subprocess.TimeoutExpired:
            return f"Error: Plugin tool '{self.name}' timed out after {_TOOL_TIMEOUT_SECONDS}s"
        except Exception as exc:
            return f"Error: Plugin tool '{self.name}' failed: {exc}"

        if proc.returncode != 0:
            stderr = proc.stderr.decode(errors="replace").strip()[:500]
            return f"Error: Plugin tool '{self.name}' exited with code {proc.returncode}: {stderr}"

        return proc.stdout.decode(errors="replace").strip()


def create_plugin_tools(
    tool_defs: list[PluginToolDef],
    plugin_name: str = "",
    plugin_root: str = "",
) -> list[BaseTool]:
    """Convert a list of plugin tool definitions into BaseTool instances."""
    tools: list[BaseTool] = []
    for td in tool_defs:
        tools.append(
            PluginTool(
                name=td.name,
                description=td.description or f"Plugin tool: {td.name}",
                command=td.command,
                plugin_name=plugin_name,
                plugin_root=plugin_root,
            )
        )
    return tools
