"""Configuration for the Swarm/Team multi-agent collaboration system."""

from pydantic import BaseModel, Field


class SwarmConfig(BaseModel):
    """Configuration for Swarm/Team mode."""

    enabled: bool = Field(default=False, description="Enable Swarm/Team mode")
    max_team_size: int = Field(default=5, ge=2, description="Maximum number of teammates per team")
    message_poll_interval_seconds: float = Field(default=2.0, gt=0, description="Seconds between inbox poll cycles")
    teammate_timeout_seconds: int = Field(default=900, ge=1, description="Default timeout for teammate execution")


_swarm_config: SwarmConfig | None = None


def get_swarm_config() -> SwarmConfig:
    global _swarm_config
    if _swarm_config is None:
        _swarm_config = SwarmConfig()
    return _swarm_config


def load_swarm_config_from_dict(data: dict) -> SwarmConfig:
    global _swarm_config
    _swarm_config = SwarmConfig.model_validate(data)
    return _swarm_config


def reset_swarm_config() -> None:
    global _swarm_config
    _swarm_config = None
