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


def serialize_message(msg: AnyMessage) -> dict:
    """Convert a LangChain message to a JSON-serializable dict."""
    return msg.model_dump() if hasattr(msg, "model_dump") else dict(msg)


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
                result[k] = [serialize_message(m) for m in v]
            elif hasattr(v, "model_dump"):
                result[k] = v.model_dump()
            else:
                json.dumps(v, default=str)
                result[k] = v
        except (TypeError, ValueError):
            logger.debug("Skipping non-serializable key %s", k)
            continue
    return result
