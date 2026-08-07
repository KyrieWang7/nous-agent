"""Configuration for agent-managed skill security scanning.

When enabled, installing a ``.skill`` archive runs every SKILL.md and support
script through an LLM security reviewer (see ``src.skills.security_scanner``).

Defaults are conservative for compatibility: scanning is **disabled by default**
so existing install flows are unchanged. Operators who want the protection turn
it on explicitly. When ``fail_open`` is True a scanner outage degrades to a
warning instead of blocking the install.
"""

from __future__ import annotations

from pydantic import BaseModel, Field


class SkillSecurityConfig(BaseModel):
    """Config section controlling skill install-time security scanning."""

    enabled: bool = Field(
        default=False,
        description="Run an LLM security scan over SKILL.md and support scripts on install.",
    )
    fail_open: bool = Field(
        default=False,
        description=(
            "When True, a scanner outage (model unavailable / unparseable) logs a "
            "warning and allows the install instead of blocking it. Only consulted "
            "when scanning is enabled."
        ),
    )
    moderation_model_name: str | None = Field(
        default=None,
        description="Model name used for the security scan. Falls back to the default model when null.",
    )


def load_skill_security_config_from_dict(data: dict | None) -> SkillSecurityConfig:
    """Build a SkillSecurityConfig from a raw config mapping (or defaults when absent)."""
    if not data:
        return SkillSecurityConfig()
    return SkillSecurityConfig(**data)
