from pathlib import Path

from pydantic import BaseModel, Field


class SkillsConfig(BaseModel):
    """Configuration for skills system"""

    path: str | None = Field(
        default=None,
        description="Path to skills directory. If not specified, defaults to ../skills relative to backend directory",
    )
    container_path: str = Field(
        default="/mnt/skills",
        description="Path where skills are mounted in the sandbox container",
    )
    slash_activation_enabled: bool = Field(
        default=True,
        description="Enable explicit /skill-name slash activation (injects SKILL.md for the turn).",
    )
    tool_policy_enabled: bool = Field(
        default=True,
        description="Restrict bound tools to the union of enabled skills' allowed-tools (no-op unless a skill declares the field).",
    )

    def get_skills_path(self) -> Path:
        """
        Get the resolved skills directory path.

        Returns:
            Path to the skills directory
        """
        if self.path:
            # Use configured path (can be absolute or relative)
            path = Path(self.path)
            if not path.is_absolute():
                # If relative, resolve from current working directory
                path = Path.cwd() / path
            return path.resolve()
        else:
            # Default: ../skills relative to backend directory
            from src.skills.loader import get_skills_root_path

            return get_skills_root_path()

    def get_skill_container_path(self, skill_name: str, category: str = "public") -> str:
        """
        Get the full container path for a specific skill.

        Args:
            skill_name: Name of the skill (directory name)
            category: Category of the skill (public or custom)

        Returns:
            Full path to the skill in the container
        """
        return f"{self.container_path}/{category}/{skill_name}"
