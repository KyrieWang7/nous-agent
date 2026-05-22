"""Local sandbox with production-grade path isolation and write protection.

Security features:
- Path traversal protection via resolved_path.relative_to(local_root)
- Read-only mount enforcement (EROFS on write to protected paths)
- Agent-written path tracking for safe reverse resolution
"""

import errno
import os
import re
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import NamedTuple

from src.sandbox.local.list_dir import list_dir
from src.sandbox.sandbox import Sandbox


@dataclass(frozen=True)
class PathMapping:
    """A path mapping from a container path to a local path with optional read-only flag.

    Attributes:
        container_path: The virtual path as seen inside the sandbox.
        local_path: The actual host filesystem path that backs the mount.
        read_only: If True, write operations to this mount raise EROFS.
    """

    container_path: str
    local_path: str
    read_only: bool = False


class ResolvedPath(NamedTuple):
    """Result of path resolution containing resolved path and its source mapping."""

    path: str
    mapping: PathMapping | None


class LocalSandbox(Sandbox):
    """Local sandbox with thread-scoped isolation and production-grade safety.

    Enforces:
    - Path traversal protection: `..` attacks blocked via resolve + relative_to
    - Read-only mounts: Protected directories cannot be written to
    - Agent-written tracking: Only agent-written files get reverse-resolved on read
    """

    def __init__(self, id: str, path_mappings: list[PathMapping] | None = None):
        """Initialize local sandbox with typed path mappings.

        Args:
            id: Unique sandbox identifier (typically thread_id or "local").
            path_mappings: List of PathMapping with container_path, local_path,
                and read_only flag.
        """
        super().__init__(id)
        self.path_mappings: list[PathMapping] = path_mappings or []
        # Track files written through write_file so read_file only
        # reverse-resolves paths in agent-authored content.
        self._agent_written_paths: set[str] = set()

    # ------------------------------------------------------------------
    # Path resolution with traversal protection
    # ------------------------------------------------------------------

    def _find_path_mapping(self, path: str) -> tuple[PathMapping, str] | None:
        """Find the most specific mapping for a given container path.

        Returns:
            Tuple of (matching mapping, relative path within the mount) or None.
        """
        path_str = str(path)
        for mapping in sorted(
            self.path_mappings,
            key=lambda m: len(m.container_path.rstrip("/") or "/"),
            reverse=True,
        ):
            container = mapping.container_path.rstrip("/") or "/"
            if container == "/":
                if path_str.startswith("/"):
                    return mapping, path_str.lstrip("/")
                continue
            if path_str == container or path_str.startswith(container + "/"):
                relative = path_str[len(container):].lstrip("/")
                return mapping, relative
        return None

    def _resolve_path_with_mapping(self, path: str) -> ResolvedPath:
        """Resolve container path to local path with traversal protection.

        Raises:
            PermissionError: If the resolved path escapes the mount root.
        """
        path_str = str(path)
        match = self._find_path_mapping(path_str)
        if match is None:
            return ResolvedPath(path_str, None)

        mapping, relative = match
        local_root = Path(mapping.local_path).resolve()
        resolved = (local_root / relative).resolve() if relative else local_root

        # Critical: prevent path traversal via .. or symlinks
        try:
            resolved.relative_to(local_root)
        except ValueError:
            raise PermissionError(
                errno.EACCES, "Access denied: path escapes mounted directory", path_str
            )

        return ResolvedPath(str(resolved), mapping)

    def _resolve_path(self, path: str) -> str:
        """Resolve container path to local path (convenience wrapper)."""
        return self._resolve_path_with_mapping(path).path

    # ------------------------------------------------------------------
    # Read-only enforcement
    # ------------------------------------------------------------------

    def _is_read_only_path(self, resolved_path: str) -> bool:
        """Check if a resolved path falls under a read-only mount.

        When multiple mappings match, the most specific one wins.
        """
        resolved = str(Path(resolved_path).resolve())
        best_mapping: PathMapping | None = None
        best_prefix_len = -1

        for mapping in self.path_mappings:
            local_resolved = str(Path(mapping.local_path).resolve())
            if resolved == local_resolved or resolved.startswith(local_resolved + os.sep):
                prefix_len = len(local_resolved)
                if prefix_len > best_prefix_len:
                    best_prefix_len = prefix_len
                    best_mapping = mapping

        if best_mapping is None:
            return False
        return best_mapping.read_only

    def _check_write_permission(self, resolved: ResolvedPath, original_path: str) -> None:
        """Raise OSError(EROFS) if the target is on a read-only mount."""
        is_ro = (resolved.mapping and resolved.mapping.read_only) or self._is_read_only_path(
            resolved.path
        )
        if is_ro:
            raise OSError(errno.EROFS, "Read-only file system", original_path)

    # ------------------------------------------------------------------
    # Reverse path resolution (local -> container)
    # ------------------------------------------------------------------

    def _reverse_resolve_path(self, path: str) -> str:
        """Reverse resolve local path back to container path."""
        normalized = path.replace("\\", "/")
        path_str = str(Path(normalized).resolve())

        for mapping in sorted(
            self.path_mappings, key=lambda m: len(m.local_path), reverse=True
        ):
            local_resolved = str(Path(mapping.local_path).resolve())
            if path_str == local_resolved or path_str.startswith(local_resolved + "/"):
                relative = path_str[len(local_resolved):].lstrip("/")
                return f"{mapping.container_path}/{relative}" if relative else mapping.container_path
        return path_str

    def _reverse_resolve_in_output(self, output: str) -> str:
        """Reverse resolve all local paths in output string to container paths."""
        sorted_mappings = sorted(
            self.path_mappings, key=lambda m: len(m.local_path), reverse=True
        )
        if not sorted_mappings:
            return output

        result = output
        for mapping in sorted_mappings:
            local_resolved = str(Path(mapping.local_path).resolve())
            escaped = re.escape(local_resolved)
            pattern = re.compile(escaped + r"(?:/[^\s\"';&|<>()]*)?")
            result = pattern.sub(
                lambda m: self._reverse_resolve_path(m.group(0)), result
            )
        return result

    # ------------------------------------------------------------------
    # Forward resolution in commands and content
    # ------------------------------------------------------------------

    def _resolve_in_command(self, command: str) -> str:
        """Resolve container paths to local paths in a shell command string."""
        sorted_mappings = sorted(
            self.path_mappings,
            key=lambda m: len(m.container_path),
            reverse=True,
        )
        if not sorted_mappings:
            return command

        patterns = [
            re.escape(m.container_path)
            + r"(?=/|$|[\s\"';&|<>()])(?:/[^\s\"';&|<>()]*)?"
            for m in sorted_mappings
        ]
        pattern = re.compile("|".join(f"({p})" for p in patterns))
        return pattern.sub(lambda m: self._resolve_path(m.group(0)), command)

    def _resolve_in_content(self, content: str) -> str:
        """Resolve container paths in file content (forward-slash safe)."""
        sorted_mappings = sorted(
            self.path_mappings,
            key=lambda m: len(m.container_path),
            reverse=True,
        )
        if not sorted_mappings:
            return content

        patterns = [
            re.escape(m.container_path) + r"(?=/|$|[^\w./-])(?:/[^\s\"';&|<>()]*)?"
            for m in sorted_mappings
        ]
        pattern = re.compile("|".join(f"({p})" for p in patterns))

        def replace_match(m: re.Match) -> str:
            resolved = self._resolve_path(m.group(0))
            return resolved.replace("\\", "/")

        return pattern.sub(replace_match, content)

    # ------------------------------------------------------------------
    # Public Sandbox API
    # ------------------------------------------------------------------

    def execute_command(self, command: str) -> str:
        """Execute a shell command with path resolution and output sanitization."""
        resolved = self._resolve_in_command(command)
        shell = shutil.which("bash") or shutil.which("sh") or "/bin/sh"

        result = subprocess.run(
            [shell, "-c", resolved],
            shell=False,
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
        """List directory contents with path resolution."""
        resolved = self._resolve_path(path)
        entries = list_dir(resolved, max_depth)
        return [self._reverse_resolve_in_output(entry) for entry in entries]

    def read_file(self, path: str) -> str:
        """Read file content, only reverse-resolving paths in agent-written files.

        Files written by the agent have their local paths reverse-resolved to
        container paths. User-uploaded files are returned as-is.
        """
        resolved = self._resolve_path(path)
        try:
            with open(resolved, encoding="utf-8") as f:
                content = f.read()
        except OSError as e:
            raise type(e)(e.errno, e.strerror, path) from None

        if resolved in self._agent_written_paths:
            content = self._reverse_resolve_in_output(content)
        return content

    def write_file(self, path: str, content: str, append: bool = False) -> None:
        """Write content to a file with read-only and traversal checks.

        Raises:
            OSError(EROFS): If the target path is on a read-only mount.
            PermissionError: If the path escapes the mounted directory.
        """
        resolved = self._resolve_path_with_mapping(path)
        self._check_write_permission(resolved, original_path=path)

        resolved_path = resolved.path
        try:
            dir_path = os.path.dirname(resolved_path)
            if dir_path:
                os.makedirs(dir_path, exist_ok=True)
            resolved_content = self._resolve_in_content(content)
            mode = "a" if append else "w"
            with open(resolved_path, mode, encoding="utf-8") as f:
                f.write(resolved_content)
            self._agent_written_paths.add(resolved_path)
        except OSError as e:
            raise type(e)(e.errno, e.strerror, path) from None

    def update_file(self, path: str, content: bytes) -> None:
        """Update a file with binary content, respecting read-only mounts.

        Raises:
            OSError(EROFS): If the target path is on a read-only mount.
            PermissionError: If the path escapes the mounted directory.
        """
        resolved = self._resolve_path_with_mapping(path)
        self._check_write_permission(resolved, original_path=path)

        resolved_path = resolved.path
        try:
            dir_path = os.path.dirname(resolved_path)
            if dir_path:
                os.makedirs(dir_path, exist_ok=True)
            with open(resolved_path, "wb") as f:
                f.write(content)
        except OSError as e:
            raise type(e)(e.errno, e.strerror, path) from None
