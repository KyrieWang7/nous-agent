"""In-memory state management for threads and runs.

Threads metadata is persisted via checkpoints; we keep a lightweight cache
so that ``GET /threads/{id}`` works without querying Postgres every time.
"""

from __future__ import annotations

import asyncio
import collections
import logging
from datetime import UTC, datetime
from typing import Any

from fastapi import HTTPException

from src.storage.database import get_checkpointer

logger = logging.getLogger(__name__)

MAX_RUN_EVENT_BUFFER = 200

_threads: dict[str, dict[str, Any]] = {}
_runs: dict[str, dict[str, Any]] = {}
_run_events: dict[str, collections.deque[dict[str, Any]]] = {}
_run_streams: dict[str, asyncio.Queue] = {}


def now_iso() -> str:
    return datetime.now(UTC).isoformat()


def thread_dict(thread_id: str, metadata: dict | None = None) -> dict:
    now = now_iso()
    return {
        "thread_id": thread_id,
        "created_at": now,
        "updated_at": now,
        "metadata": metadata or {},
        "status": "idle",
        "values": {},
        "config": {"configurable": {"thread_id": thread_id}},
    }


async def ensure_thread(thread_id: str) -> dict:
    """Get or create a thread entry, resurrecting from checkpoints if needed."""
    if thread_id in _threads:
        return _threads[thread_id]

    checkpointer = await get_checkpointer()
    config = {"configurable": {"thread_id": thread_id}}
    try:
        cp = await checkpointer.aget_tuple(config)
    except Exception:
        cp = None

    if cp is not None:
        t = thread_dict(thread_id, (cp.metadata or {}).copy())
        _threads[thread_id] = t
        logger.info("Resurrected thread %s from checkpoint", thread_id)
        return t

    raise HTTPException(status_code=404, detail=f"Thread {thread_id} not found")


def get_threads() -> dict[str, dict[str, Any]]:
    return _threads


def get_runs() -> dict[str, dict[str, Any]]:
    return _runs


def get_run_events() -> dict[str, collections.deque[dict[str, Any]]]:
    return _run_events


def get_run_streams() -> dict[str, asyncio.Queue]:
    return _run_streams


def buffer_run_event(run_id: str, event: dict[str, Any]) -> None:
    """Append an event to the run's buffer, respecting the size limit."""
    buf = _run_events.setdefault(run_id, collections.deque(maxlen=MAX_RUN_EVENT_BUFFER))
    buf.append(event)
