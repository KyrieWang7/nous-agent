"""Thread CRUD endpoints (LangGraph Platform API)."""

from __future__ import annotations

import uuid
from typing import Any

from fastapi import APIRouter
from pydantic import BaseModel, Field

from src.core.state import ensure_thread, get_threads, now_iso, thread_dict

router = APIRouter(prefix="/threads", tags=["Threads"])


class ThreadCreate(BaseModel):
    thread_id: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)
    if_exists: str | None = None


class ThreadUpdate(BaseModel):
    metadata: dict[str, Any] | None = None


@router.post("")
async def create_thread(body: ThreadCreate):
    threads = get_threads()
    thread_id = body.thread_id or str(uuid.uuid4())

    if body.if_exists == "do_nothing" and thread_id in threads:
        return threads[thread_id]

    t = thread_dict(thread_id, body.metadata)
    threads[thread_id] = t
    return t


@router.get("/{thread_id}")
async def get_thread(thread_id: str):
    return await ensure_thread(thread_id)


@router.patch("/{thread_id}")
async def update_thread(thread_id: str, body: ThreadUpdate):
    t = await ensure_thread(thread_id)
    if body.metadata is not None:
        t["metadata"].update(body.metadata)
    t["updated_at"] = now_iso()
    return t


@router.delete("/{thread_id}")
async def delete_thread(thread_id: str):
    get_threads().pop(thread_id, None)
    return {"ok": True}


@router.post("/search")
async def search_threads():
    return list(get_threads().values())
