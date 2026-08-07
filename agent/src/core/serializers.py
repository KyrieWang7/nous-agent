"""Serialization helpers for LangChain messages and thread state values."""

from __future__ import annotations

import json
import logging
from typing import Any

from langchain_core.messages import AnyMessage

logger = logging.getLogger(__name__)

_SKIP_KEYS = frozenset({
    "__pregel_tasks",
    "__pregel_pull",
    "__pregel_push",
    "branch:to:model",
    "_summarization_event",
})


def serialize_message(msg: AnyMessage) -> dict | None:
    """Convert a LangChain message to a JSON-serializable dict.

    Returns ``None`` for values that are not message-like (e.g. bare strings
    or other primitives that occasionally appear in stream chunks), since
    ``dict()`` on those raises ``ValueError``/``TypeError``.
    """
    if hasattr(msg, "model_dump"):
        return msg.model_dump()
    if isinstance(msg, dict):
        return msg
    logger.debug("Skipping non-message stream chunk of type %s", type(msg).__name__)
    return None


def serialize_state_values(values: dict[str, Any] | None) -> dict[str, Any]:
    """Serialise thread state values, handling message objects and
    filtering out internal LangGraph objects that aren't JSON-safe."""
    if not values:
        return {}
    result: dict[str, Any] = {}
    for k, v in values.items():
        if k in _SKIP_KEYS or k.startswith("_"):
            continue
        try:
            if k == "messages" and isinstance(v, list):
                result[k] = [m for m in (serialize_message(msg) for msg in v) if m is not None]
            elif hasattr(v, "model_dump"):
                result[k] = v.model_dump()
            else:
                json.dumps(v, default=str)
                result[k] = v
        except (TypeError, ValueError):
            logger.debug("Skipping non-serializable key %s", k)
            continue
    return result
