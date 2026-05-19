"""Abstract base class for sandbox environments."""

from abc import ABC, abstractmethod


class Sandbox(ABC):
    """Abstract base class for sandbox environments."""

    _id: str

    def __init__(self, id: str):
        self._id = id

    @property
    def id(self) -> str:
        return self._id

    @abstractmethod
    def execute_command(self, command: str) -> str:
        """Execute bash command in sandbox."""
        pass

    @abstractmethod
    def read_file(self, path: str) -> str:
        """Read the content of a file."""
        pass

    @abstractmethod
    def list_dir(self, path: str, max_depth: int = 2) -> list[str]:
        """List the contents of a directory."""
        pass

    @abstractmethod
    def write_file(self, path: str, content: str, append: bool = False) -> None:
        """Write content to a file."""
        pass

    @abstractmethod
    def update_file(self, path: str, content: bytes) -> None:
        """Update a file with binary content."""
        pass
