"""Gateway thread metadata API used by the workspace sidebar."""

from __future__ import annotations

import logging
import uuid
from datetime import datetime
from typing import Any

from fastapi import APIRouter, HTTPException, Query
from pydantic import BaseModel, Field
from src.storage.session_manager import DEFAULT_LOCAL_USER_ID, SessionManager

router = APIRouter(prefix="/api", tags=["threads"])
logger = logging.getLogger(__name__)

class ThreadCreate(BaseModel):
    id: str = Field(default_factory=lambda: str(uuid.uuid4()))
    title: str | None = None
    model_name: str | None = None


class ThreadUpdate(BaseModel):
    title: str | None = None
    model_name: str | None = None
    is_archived: bool | None = None


class ThreadResponse(BaseModel):
    id: str
    title: str | None = None
    model_name: str | None = None
    created_at: datetime
    updated_at: datetime
    is_archived: bool = False
    message_count: int = 0


class ThreadListResponse(BaseModel):
    threads: list[ThreadResponse]
    total: int


def _thread_response(row: dict[str, Any]) -> ThreadResponse:
    thread_id = row.get("id") or row.get("thread_id")
    return ThreadResponse(
        id=str(thread_id),
        title=row.get("title"),
        model_name=row.get("model_name"),
        created_at=row["created_at"],
        updated_at=row["updated_at"],
        is_archived=bool(row.get("is_archived", False)),
        message_count=int(row.get("message_count") or 0),
    )


@router.get("/threads", response_model=ThreadListResponse)
async def list_threads(
    limit: int = Query(default=50, ge=1, le=100),
    offset: int = Query(default=0, ge=0),
    include_archived: bool = False,
) -> ThreadListResponse:
    try:
        rows, total = await SessionManager.list_sessions(
            DEFAULT_LOCAL_USER_ID,
            limit=limit,
            offset=offset,
            include_archived=include_archived,
        )
    except Exception:
        logger.warning("Failed to list threads; returning empty list", exc_info=True)
        return ThreadListResponse(threads=[], total=0)
    return ThreadListResponse(
        threads=[_thread_response(row) for row in rows],
        total=total,
    )


@router.post("/threads", response_model=ThreadResponse)
async def create_thread(body: ThreadCreate) -> ThreadResponse:
    row = await SessionManager.create_session(
        body.id,
        DEFAULT_LOCAL_USER_ID,
        title=body.title,
        model_name=body.model_name,
    )
    return _thread_response(row)


@router.patch("/threads/{thread_id}", response_model=ThreadResponse)
async def update_thread(thread_id: str, body: ThreadUpdate) -> ThreadResponse:
    row = await SessionManager.update_session(
        thread_id,
        DEFAULT_LOCAL_USER_ID,
        title=body.title,
        model_name=body.model_name,
        is_archived=body.is_archived,
    )
    if row is None:
        raise HTTPException(status_code=404, detail=f"Thread {thread_id} not found")
    return _thread_response(row)


@router.delete("/threads/{thread_id}")
async def delete_thread(thread_id: str) -> dict[str, bool]:
    deleted = await SessionManager.delete_session(thread_id, DEFAULT_LOCAL_USER_ID)
    if not deleted:
        raise HTTPException(status_code=404, detail=f"Thread {thread_id} not found")
    return {"ok": True}
