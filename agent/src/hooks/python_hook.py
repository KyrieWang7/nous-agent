"""Python callable hook executor.

Allows hooks to be defined as Python functions (resolved via ``module:attr``
notation) instead of external shell commands.
"""

from __future__ import annotations

import logging
from collections.abc import Callable

from src.hooks.types import HookPayload, HookResult

logger = logging.getLogger(__name__)

HookCallable = Callable[[HookPayload], HookResult | None]


def run_python_hook(func: HookCallable, payload: HookPayload) -> HookResult:
    """Invoke a Python hook function and normalise its return value."""
    try:
        result = func(payload)
        if result is None:
            return HookResult.allowed()
        if isinstance(result, HookResult):
            return result
        logger.warning("Python hook returned unexpected type %s; treating as allow", type(result).__name__)
        return HookResult.allowed()
    except Exception as exc:
        logger.exception("Python hook raised: %s", exc)
        return HookResult.warned(f"Python hook error: {exc}")


def resolve_python_hook(dotted_path: str) -> HookCallable:
    """Resolve ``module:attr`` to a callable.

    Raises ``ImportError`` or ``AttributeError`` on failure.
    """
    from src.reflection import resolve_variable

    return resolve_variable(dotted_path)
