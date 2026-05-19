"""Server info and health check endpoints."""

from __future__ import annotations

from fastapi import APIRouter

router = APIRouter(tags=["Info"])


@router.get("/info")
async def get_info():
    return {
        "version": "0.1.0",
        "langgraph_py_version": "1.0.10",
        "flags": {
            "assistants": True,
            "crons": False,
            "langsmith": False,
        },
        "host": {
            "kind": "self-hosted",
        },
    }


@router.get("/ok")
async def health():
    return {"ok": True}
