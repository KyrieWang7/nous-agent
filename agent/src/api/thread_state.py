"""Thread state and history endpoints (LangGraph Platform API)."""

from __future__ import annotations

from typing import Any

from fastapi import APIRouter, Request
from pydantic import BaseModel

from src.core.serializers import serialize_state_values
from src.core.state import now_iso
from src.core.stream import estimate_token_usage
from src.storage.database import get_checkpointer

router = APIRouter(prefix="/threads", tags=["Thread State"])


class StateUpdate(BaseModel):
    values: dict[str, Any] | None = None
    as_node: str | None = None
    checkpoint: dict[str, Any] | None = None


@router.get("/{thread_id}/state")
async def get_thread_state(thread_id: str):
    checkpointer = await get_checkpointer()
    config = {"configurable": {"thread_id": thread_id}}

    cp = await checkpointer.aget_tuple(config)
    if cp is None:
        return {
            "values": {},
            "next": [],
            "checkpoint": {
                "thread_id": thread_id,
                "checkpoint_ns": "",
                "checkpoint_id": "",
            },
            "metadata": {},
            "created_at": now_iso(),
            "parent_checkpoint": None,
            "tasks": [],
        }

    values = cp.checkpoint.get("channel_values", {})
    serialized = serialize_state_values(values)

    token_sse = estimate_token_usage(serialized)
    token_usage = token_sse["data"] if token_sse else None

    return {
        "values": serialized,
        "next": [],
        "checkpoint": {
            "thread_id": thread_id,
            "checkpoint_ns": cp.config.get("configurable", {}).get("checkpoint_ns", ""),
            "checkpoint_id": cp.config.get("configurable", {}).get("checkpoint_id", ""),
        },
        "metadata": cp.metadata or {},
        "created_at": cp.checkpoint.get("ts", now_iso()),
        "parent_checkpoint": {
            "thread_id": thread_id,
            "checkpoint_ns": cp.parent_config.get("configurable", {}).get("checkpoint_ns", "") if cp.parent_config else "",
            "checkpoint_id": cp.parent_config.get("configurable", {}).get("checkpoint_id", "") if cp.parent_config else "",
        } if cp.parent_config else None,
        "tasks": [],
        "token_usage": token_usage,
    }


@router.post("/{thread_id}/state")
async def update_thread_state(thread_id: str, body: StateUpdate):
    """Update thread state (e.g. for title rename)."""
    checkpointer = await get_checkpointer()
    config = {"configurable": {"thread_id": thread_id}}

    from langgraph.graph import StateGraph

    from src.agents.thread_state import ThreadState

    graph = StateGraph(ThreadState)
    graph.add_node("_state_updater", lambda x: x)
    graph.set_entry_point("_state_updater")
    compiled = graph.compile(checkpointer=checkpointer)

    await compiled.aupdate_state(
        config,
        body.values or {},
        as_node=body.as_node if body.as_node and body.as_node != "__start__" else "_state_updater",
    )
    return await get_thread_state(thread_id)


@router.post("/{thread_id}/history")
async def get_thread_history_post(thread_id: str, request: Request):
    """Return state history for a thread (POST variant)."""
    limit = 10
    try:
        body = await request.json()
        limit = body.get("limit", 10)
    except Exception:
        pass
    return await _get_history(thread_id, limit)


@router.get("/{thread_id}/history")
async def get_thread_history_get(thread_id: str):
    """Return state history for a thread (GET variant)."""
    return await _get_history(thread_id, 10)


async def _get_history(thread_id: str, limit: int) -> list[dict]:
    checkpointer = await get_checkpointer()
    config = {"configurable": {"thread_id": thread_id}}

    states: list[dict] = []
    count = 0
    async for cp in checkpointer.alist(config, limit=limit):
        values = cp.checkpoint.get("channel_values", {})
        parent_ckpt = None
        if cp.parent_config:
            pc = cp.parent_config.get("configurable", {}) if isinstance(cp.parent_config, dict) else {}
            parent_ckpt = {
                "thread_id": pc.get("thread_id", thread_id),
                "checkpoint_ns": pc.get("checkpoint_ns", ""),
                "checkpoint_id": pc.get("checkpoint_id", ""),
            }
        states.append({
            "values": serialize_state_values(values),
            "next": [],
            "checkpoint": {
                "thread_id": thread_id,
                "checkpoint_ns": cp.config.get("configurable", {}).get("checkpoint_ns", ""),
                "checkpoint_id": cp.config.get("configurable", {}).get("checkpoint_id", ""),
            },
            "metadata": cp.metadata or {},
            "created_at": cp.checkpoint.get("ts", now_iso()),
            "parent_checkpoint": parent_ckpt,
            "tasks": [],
        })
        count += 1
        if count >= limit:
            break

    return states
