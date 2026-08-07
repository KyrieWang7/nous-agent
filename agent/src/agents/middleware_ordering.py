"""Declarative insertion of extra middlewares using @Next/@Prev anchors.

Ported from deer-flow (deerflow.agents.factory._insert_extra), adapted to the
nous-agent middleware chain whose terminal middleware is ClarificationMiddleware.

The base chain is assembled explicitly by ``_build_middlewares``. Extra
middlewares (from plugins or SDK callers) are inserted here based on their
declared anchors:

- ``@Next(X)``  → immediately after the first instance of X,
- ``@Prev(X)``  → immediately before the first instance of X,
- unanchored    → before the terminal ClarificationMiddleware.
"""

from __future__ import annotations

import logging

from langchain.agents.middleware import AgentMiddleware

logger = logging.getLogger(__name__)


def insert_extra_middlewares(chain: list[AgentMiddleware], extras: list[AgentMiddleware]) -> None:
    """Insert *extras* into *chain* in place using @Next/@Prev anchors.

    Algorithm:
      1. Validate: no middleware declares both @Next and @Prev.
      2. Conflict detection: two extras targeting the same anchor → error.
      3. Insert unanchored extras before the terminal ClarificationMiddleware
         (or at the end if no Clarification middleware is present).
      4. Insert anchored extras iteratively (supports extra-to-extra anchoring).
      5. If an anchor can never be resolved → error.

    Raises:
        ValueError: on conflicting declarations, circular dependencies, or
            unresolvable anchors.
    """
    if not extras:
        return

    # Lazy import to avoid a hard module-load cycle with the agent package.
    from src.agents.middlewares.clarification_middleware import ClarificationMiddleware

    next_targets: dict[type, type] = {}
    prev_targets: dict[type, type] = {}

    anchored: list[tuple[AgentMiddleware, str, type]] = []
    unanchored: list[AgentMiddleware] = []

    for mw in extras:
        next_anchor = getattr(type(mw), "_next_anchor", None)
        prev_anchor = getattr(type(mw), "_prev_anchor", None)

        if next_anchor and prev_anchor:
            raise ValueError(f"{type(mw).__name__} cannot declare both @Next and @Prev")

        if next_anchor:
            if next_anchor in next_targets:
                raise ValueError(f"Conflict: {type(mw).__name__} and {next_targets[next_anchor].__name__} both @Next({next_anchor.__name__})")
            if next_anchor in prev_targets:
                raise ValueError(f"Conflict: {type(mw).__name__} @Next({next_anchor.__name__}) and {prev_targets[next_anchor].__name__} @Prev({next_anchor.__name__}) — use cross-anchoring between extras instead")
            next_targets[next_anchor] = type(mw)
            anchored.append((mw, "next", next_anchor))
        elif prev_anchor:
            if prev_anchor in prev_targets:
                raise ValueError(f"Conflict: {type(mw).__name__} and {prev_targets[prev_anchor].__name__} both @Prev({prev_anchor.__name__})")
            if prev_anchor in next_targets:
                raise ValueError(f"Conflict: {type(mw).__name__} @Prev({prev_anchor.__name__}) and {next_targets[prev_anchor].__name__} @Next({prev_anchor.__name__}) — use cross-anchoring between extras instead")
            prev_targets[prev_anchor] = type(mw)
            anchored.append((mw, "prev", prev_anchor))
        else:
            unanchored.append(mw)

    # Unanchored → before the terminal ClarificationMiddleware (or end of chain).
    insert_idx = next((i for i, m in enumerate(chain) if isinstance(m, ClarificationMiddleware)), len(chain))
    for mw in unanchored:
        chain.insert(insert_idx, mw)
        insert_idx += 1

    # Anchored → iterative insertion (supports extra-to-extra anchoring).
    pending = list(anchored)
    max_rounds = len(pending) + 1
    for _ in range(max_rounds):
        if not pending:
            break
        remaining: list[tuple[AgentMiddleware, str, type]] = []
        for mw, direction, anchor in pending:
            idx = next((i for i, m in enumerate(chain) if isinstance(m, anchor)), None)
            if idx is None:
                remaining.append((mw, direction, anchor))
                continue
            if direction == "next":
                chain.insert(idx + 1, mw)
            else:
                chain.insert(idx, mw)
        if len(remaining) == len(pending):
            names = [type(m).__name__ for m, _, _ in remaining]
            anchor_types = {a for _, _, a in remaining}
            remaining_types = {type(m) for m, _, _ in remaining}
            circular = anchor_types & remaining_types
            if circular:
                raise ValueError(f"Circular dependency among extra middlewares: {', '.join(t.__name__ for t in circular)}")
            raise ValueError(
                f"Cannot resolve positions for {', '.join(names)} — "
                f"anchors {', '.join(a.__name__ for _, _, a in remaining)} not found in chain"
            )
        pending = remaining
