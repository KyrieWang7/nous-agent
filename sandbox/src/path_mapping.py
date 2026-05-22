"""Path mapping data structures for sandbox mount configuration."""

from dataclasses import dataclass


@dataclass(frozen=True)
class PathMapping:
    """A path mapping from a container path to a local path with optional read-only flag.

    Attributes:
        container_path: The virtual path as seen inside the sandbox
            (e.g., /mnt/user-data/workspace).
        local_path: The actual host filesystem path that backs the mount.
        read_only: If True, write operations to this mount raise EROFS.
            Useful for protecting shared resources like skills directories.
    """

    container_path: str
    local_path: str
    read_only: bool = False
