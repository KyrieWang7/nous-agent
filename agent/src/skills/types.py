from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path

SKILL_MD_FILE = "SKILL.md"


class SkillCategory(StrEnum):
    """Source category for a skill.

    - ``PUBLIC``: built-in skill bundled with the platform, read-only.
    - ``CUSTOM``: user-authored skill that can be edited or deleted.

    ``StrEnum`` subclasses ``str``, so existing code that compares / serialises
    ``skill.category`` as a plain string (e.g. ``"public"``) keeps working.
    """

    PUBLIC = "public"
    CUSTOM = "custom"


@dataclass
class Skill:
    """Represents a skill with its metadata and file path"""

    name: str
    description: str
    license: str | None
    skill_dir: Path
    skill_file: Path
    category: SkillCategory  # 'public' or 'custom'
    # Relative path from the category root (skills/<category>) to the skill
    # directory. Defaults to the skill directory name for flat layouts.
    relative_path: Path | None = None
    # Explicit tool allowlist declared in frontmatter (``allowed-tools``).
    # ``None`` means "not declared" (legacy allow-all); an empty list means the
    # skill explicitly declares no tools.
    allowed_tools: list[str] | None = None
    enabled: bool = False  # Whether this skill is enabled

    @property
    def skill_path(self) -> str:
        """Returns the relative path from the category root to this skill's directory."""
        if self.relative_path is not None:
            path = self.relative_path.as_posix()
            return "" if path == "." else path
        # Back-compat fallback: flat layout uses the directory name.
        return self.skill_dir.name

    def get_container_path(self, container_base_path: str = "/mnt/skills") -> str:
        """
        Get the full path to this skill in the container.

        Args:
            container_base_path: Base path where skills are mounted in the container

        Returns:
            Full container path to the skill directory
        """
        category_base = f"{container_base_path}/{self.category}"
        skill_path = self.skill_path
        if skill_path:
            return f"{category_base}/{skill_path}"
        return category_base

    def get_container_file_path(self, container_base_path: str = "/mnt/skills") -> str:
        """
        Get the full path to this skill's main file (SKILL.md) in the container.

        Args:
            container_base_path: Base path where skills are mounted in the container

        Returns:
            Full container path to the skill's SKILL.md file
        """
        return f"{self.get_container_path(container_base_path)}/SKILL.md"

    def __repr__(self) -> str:
        return f"Skill(name={self.name!r}, description={self.description!r}, category={self.category!r})"
