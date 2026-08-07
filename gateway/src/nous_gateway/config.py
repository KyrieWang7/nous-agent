import os
from pathlib import Path

from pydantic import BaseModel, Field


class GatewayConfig(BaseModel):
    """Configuration for the API Gateway."""

    host: str = Field(default="0.0.0.0", description="Host to bind the gateway server")
    port: int = Field(default=7777, description="Port to bind the gateway server")
    cors_origins: list[str] = Field(default_factory=lambda: ["http://localhost:3000"], description="Allowed CORS origins")


_gateway_config: GatewayConfig | None = None


def configure_product_paths() -> None:
    """Resolve sibling product resources for a source or editable install.

    Explicit environment variables always win. Packaged deployments that do
    not include the sibling `agent/` directory must provide these paths.
    """
    repository_root = Path(__file__).resolve().parents[3]
    agent_root = repository_root / "agent"
    config_path = agent_root / "config.yaml"
    extensions_path = agent_root / "extensions_config.json"
    if config_path.is_file():
        os.environ.setdefault("DEER_FLOW_CONFIG_PATH", str(config_path))
    if extensions_path.is_file():
        os.environ.setdefault("DEER_FLOW_EXTENSIONS_CONFIG_PATH", str(extensions_path))


def get_gateway_config() -> GatewayConfig:
    """Get gateway config, loading from environment if available."""
    global _gateway_config
    if _gateway_config is None:
        cors_origins_str = os.getenv("CORS_ORIGINS", "http://localhost:3000")
        _gateway_config = GatewayConfig(
            host=os.getenv("GATEWAY_HOST", "0.0.0.0"),
            port=int(os.getenv("GATEWAY_PORT", "7777")),
            cors_origins=cors_origins_str.split(","),
        )
    return _gateway_config
