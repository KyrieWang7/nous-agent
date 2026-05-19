"""SSE event formatting utilities."""

from __future__ import annotations

import json
from typing import Any


def format_sse(event: str, data: Any) -> str:
    """Format a single SSE event string.

    Returns a string in the form::

        event: <event>
        data: <json-encoded data>

    with a trailing blank line as required by the SSE spec.
    """
    if isinstance(data, str):
        data_str = data
    else:
        data_str = json.dumps(data, default=str, ensure_ascii=False)
    return f"event: {event}\ndata: {data_str}\n\n"


SSE_PING = ": ping\n\n"
