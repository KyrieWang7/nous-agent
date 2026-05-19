"""LangGraph stream transformer.

Converts raw ``astream`` output into the SSE event dicts expected by
the frontend client.

When ``stream_mode`` is a list, ``astream`` yields tuples:
- ``(mode, data)`` if ``subgraphs=False``
- ``(namespace, mode, data)`` if ``subgraphs=True``

When ``stream_mode`` is a single string, ``astream`` yields plain data.
"""

from __future__ import annotations

import logging
from typing import Any

import tiktoken

from src.core.serializers import serialize_message, serialize_state_values

logger = logging.getLogger(__name__)

_enc = tiktoken.get_encoding("cl100k_base")


def _count_tokens(text: str) -> int:
    if not text:
        return 0
    return len(_enc.encode(text))


def _extract_text(content: Any) -> str:
    """Extract plain text from a message content field (str or list of parts)."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for p in content:
            if isinstance(p, dict) and p.get("type") == "text":
                parts.append(p.get("text", ""))
            elif isinstance(p, str):
                parts.append(p)
        return " ".join(parts)
    return ""


def estimate_token_usage(values_data: dict[str, Any]) -> dict[str, Any] | None:
    """Estimate token usage from serialized messages using tiktoken."""
    messages = values_data.get("messages")
    if not messages:
        return None

    input_tokens = 0
    output_tokens = 0

    for msg in messages:
        text = _extract_text(msg.get("content", ""))
        reasoning = (msg.get("additional_kwargs") or {}).get("reasoning_content", "")
        tokens = _count_tokens(text) + _count_tokens(reasoning)

        if msg.get("type") == "ai":
            output_tokens += tokens
        else:
            input_tokens += tokens

    total = input_tokens + output_tokens
    if total == 0:
        return None

    return {
        "event": "custom",
        "data": {
            "type": "token_usage",
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "cache_read_tokens": 0,
            "total_tokens": total,
        },
    }


def transform_astream_chunk(
    chunk: Any,
    stream_mode: list[str],
    subgraphs: bool = False,
) -> dict[str, Any] | None:
    """Map a single ``astream`` chunk to an SSE payload.

    Returns ``None`` when the chunk should be skipped.
    """
    if isinstance(stream_mode, list) and len(stream_mode) > 1:
        if subgraphs:
            if not isinstance(chunk, tuple) or len(chunk) < 3:
                return None
            _ns, mode, data = chunk[0], chunk[1], chunk[2]
        else:
            if not isinstance(chunk, tuple) or len(chunk) < 2:
                return None
            mode, data = chunk[0], chunk[1]
    else:
        mode = stream_mode[0] if isinstance(stream_mode, list) else stream_mode
        data = chunk

    return _map_mode_data(mode, data)


def _map_mode_data(mode: str, data: Any) -> dict[str, Any] | None:
    """Convert a (mode, data) pair into an SSE event dict."""
    if mode == "values":
        if isinstance(data, dict):
            return {"event": "values", "data": serialize_state_values(data)}

    elif mode == "messages":
        if isinstance(data, (list, tuple)) and len(data) >= 1:
            msg_chunk = data[0]
            if msg_chunk is None:
                return None
            meta = data[1] if len(data) > 1 and isinstance(data[1], dict) else {}
            serialized = serialize_message(msg_chunk)
            if serialized:
                return {"event": "messages", "data": [serialized, meta]}

    elif mode == "custom":
        return {"event": "custom", "data": data}

    return None
