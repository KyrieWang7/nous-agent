"""Local sandbox - executes commands directly on the host."""

import os
import re
import shutil
import subprocess
from pathlib import Path

from src.sandbox import Sandbox


def _list_dir_tree(path: str, max_depth: int = 2, current_depth: int = 0) -> list[str]:
    """List directory contents in tree format."""
    entries = []
    try:
        items = sorted(Path(path).iterdir(), key=lambda x: (not x.is_dir(), x.name))
    except PermissionError:
        return [f"{path}: Permission denied"]

    for item in items:
        prefix = "  " * current_depth
        if item.is_dir():
            entries.append(f"{prefix}{item.name}/")
            if current_depth < max_depth - 1:
                entries.extend(_list_dir_tree(str(item), max_depth, current_depth + 1))
        else:
            entries.append(f"{prefix}{item.name}")
    return entries


class LocalSandbox(Sandbox):
    """Local sandbox that executes commands directly on the host machine."""

    def __init__(self, id: str, path_mappings: dict[str, str] | None = None):
        super().__init__(id)
        self.path_mappings = path_mappings or {}

    def _resolve_path(self, path: str) -> str:
        """Resolve container path to actual local path using mappings."""
        path_str = str(path)
        for container_path, local_path in sorted(
            self.path_mappings.items(), key=lambda x: len(x[0]), reverse=True
        ):
            if path_str.startswith(container_path):
                relative = path_str[len(container_path):].lstrip("/")
                return str(Path(local_path) / relative) if relative else local_path
        return path_str

    def _reverse_resolve_path(self, path: str) -> str:
        """Reverse resolve local path back to container path."""
        path_str = str(Path(path).resolve())
        for container_path, local_path in sorted(
            self.path_mappings.items(), key=lambda x: len(x[1]), reverse=True
        ):
            local_resolved = str(Path(local_path).resolve())
            if path_str.startswith(local_resolved):
                relative = path_str[len(local_resolved):].lstrip("/")
                return f"{container_path}/{relative}" if relative else container_path
        return path_str

    def _reverse_resolve_in_output(self, output: str) -> str:
        """Reverse resolve local paths in output string."""
        sorted_mappings = sorted(
            self.path_mappings.items(), key=lambda x: len(x[1]), reverse=True
        )
        if not sorted_mappings:
            return output

        result = output
        for container_path, local_path in sorted_mappings:
            local_resolved = str(Path(local_path).resolve())
            escaped = re.escape(local_resolved)
            pattern = re.compile(escaped + r"(?:/[^\s\"';&|<>()]*)?")
            result = pattern.sub(lambda m: self._reverse_resolve_path(m.group(0)), result)
        return result

    def _resolve_in_command(self, command: str) -> str:
        """Resolve container paths to local paths in command."""
        sorted_mappings = sorted(
            self.path_mappings.items(), key=lambda x: len(x[0]), reverse=True
        )
        if not sorted_mappings:
            return command

        patterns = [
            re.escape(cp) + r"(?:/[^\s\"';&|<>()]*)?"
            for cp, _ in sorted_mappings
        ]
        pattern = re.compile("|".join(f"({p})" for p in patterns))
        return pattern.sub(lambda m: self._resolve_path(m.group(0)), command)

    def execute_command(self, command: str) -> str:
        resolved = self._resolve_in_command(command)
        shell = shutil.which("bash") or shutil.which("sh") or "/bin/sh"

        result = subprocess.run(
            resolved,
            executable=shell,
            shell=True,
            capture_output=True,
            text=True,
            timeout=600,
        )
        output = result.stdout
        if result.stderr:
            output += f"\nStd Error:\n{result.stderr}" if output else result.stderr
        if result.returncode != 0:
            output += f"\nExit Code: {result.returncode}"

        final = output if output else "(no output)"
        return self._reverse_resolve_in_output(final)

    def list_dir(self, path: str, max_depth: int = 2) -> list[str]:
        resolved = self._resolve_path(path)
        entries = _list_dir_tree(resolved, max_depth)
        return [self._reverse_resolve_in_output(e) for e in entries]

    def read_file(self, path: str) -> str:
        resolved = self._resolve_path(path)
        with open(resolved) as f:
            return f.read()

    def write_file(self, path: str, content: str, append: bool = False) -> None:
        resolved = self._resolve_path(path)
        dir_path = os.path.dirname(resolved)
        if dir_path:
            os.makedirs(dir_path, exist_ok=True)
        with open(resolved, "a" if append else "w") as f:
            f.write(content)

    def update_file(self, path: str, content: bytes) -> None:
        resolved = self._resolve_path(path)
        dir_path = os.path.dirname(resolved)
        if dir_path:
            os.makedirs(dir_path, exist_ok=True)
        with open(resolved, "wb") as f:
            f.write(content)
