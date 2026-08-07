"""Declarative middleware positioning helpers for the lead agent.

Ported from deer-flow (deerflow.agents.features). Pure data/decorators — no I/O.

The lead agent assembles a fixed base middleware chain in code. To let plugins
and SDK callers inject *extra* middlewares at well-defined positions without
hand-editing the chain, an extra middleware class may declare where it goes
relative to an existing middleware using the ``@Next`` / ``@Prev`` decorators::

    @Next(ToolErrorHandlingMiddleware)
    class MyAuditMiddleware(AgentMiddleware):
        ...

``@Next(X)`` places the middleware immediately *after* X; ``@Prev(X)`` places it
immediately *before* X. Unanchored extras are inserted before the terminal
``ClarificationMiddleware``. See ``insert_extra_middlewares``.
"""

from __future__ import annotations

from dataclasses import dataclass

from langchain.agents.middleware import AgentMiddleware


@dataclass
class RuntimeFeatures:
    """Declarative feature toggles for the lead agent middleware chain.

    Each flag accepts:
    - ``True``: use the built-in default middleware (when one exists),
    - ``False``: disable the feature,
    - an ``AgentMiddleware`` instance: use this custom implementation instead.

    The lead agent reads these to decide whether to add / replace specific
    middlewares. Flags left at their defaults preserve current behaviour.
    """

    sandbox: bool | AgentMiddleware = True
    memory: bool | AgentMiddleware = True
    summarization: bool | AgentMiddleware = True
    title: bool | AgentMiddleware = True
    vision: bool | AgentMiddleware = True
    loop_detection: bool | AgentMiddleware = True
    tool_output_budget: bool | AgentMiddleware = True
    safety_finish_reason: bool | AgentMiddleware = True
    skill_activation: bool | AgentMiddleware = True


# ---------------------------------------------------------------------------
# Middleware positioning decorators
# ---------------------------------------------------------------------------


def Next(anchor: type[AgentMiddleware]):
    """Declare this middleware should be placed immediately after *anchor*."""
    if not (isinstance(anchor, type) and issubclass(anchor, AgentMiddleware)):
        raise TypeError(f"@Next expects an AgentMiddleware subclass, got {anchor!r}")

    def decorator(cls: type[AgentMiddleware]) -> type[AgentMiddleware]:
        cls._next_anchor = anchor  # type: ignore[attr-defined]
        return cls

    return decorator


def Prev(anchor: type[AgentMiddleware]):
    """Declare this middleware should be placed immediately before *anchor*."""
    if not (isinstance(anchor, type) and issubclass(anchor, AgentMiddleware)):
        raise TypeError(f"@Prev expects an AgentMiddleware subclass, got {anchor!r}")

    def decorator(cls: type[AgentMiddleware]) -> type[AgentMiddleware]:
        cls._prev_anchor = anchor  # type: ignore[attr-defined]
        return cls

    return decorator
