"""Sandbox client - provides sandbox access for the backend agent."""

import logging
from typing import Any

logger = logging.getLogger(__name__)

# Re-export from the sandbox package for backward compatibility
# In the new architecture, Backend imports sandbox abstractions from packages/sandbox
# For now, this module bridges the gap

_sandbox_provider = None


def get_sandbox_provider(**kwargs):
    """Get the sandbox provider singleton.

    This bridges the backend to the sandbox package.
    Configuration determines which provider to use.
    """
    global _sandbox_provider
    if _sandbox_provider is None:
        from src.config.app_config import get_app_config
        config = get_app_config()
        sandbox_config = config.sandbox

        # Import and instantiate the configured provider
        use = sandbox_config.use if hasattr(sandbox_config, 'use') else 'src.sandbox_client.local:LocalSandboxProvider'

        if 'aio_sandbox' in use or 'AioSandboxProvider' in use:
            from packages.sandbox.src.providers.aio_provider import AioSandboxProvider
            _sandbox_provider = AioSandboxProvider(
                base_url=getattr(sandbox_config, 'base_url', 'http://localhost:8080'),
                auto_start=getattr(sandbox_config, 'auto_start', True),
            )
        else:
            from packages.sandbox.src.providers.local_provider import LocalSandboxProvider
            _sandbox_provider = LocalSandboxProvider(**kwargs)

    return _sandbox_provider


def reset_sandbox_provider():
    """Reset the sandbox provider singleton."""
    global _sandbox_provider
    _sandbox_provider = None


def shutdown_sandbox_provider():
    """Shutdown and reset the sandbox provider."""
    global _sandbox_provider
    if _sandbox_provider is not None:
        if hasattr(_sandbox_provider, 'shutdown'):
            _sandbox_provider.shutdown()
        _sandbox_provider = None
