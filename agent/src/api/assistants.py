"""Assistants CRUD endpoints (LangGraph Platform API)."""

from __future__ import annotations

from fastapi import APIRouter

from src.core.state import now_iso

router = APIRouter(prefix="/assistants", tags=["Assistants"])


@router.post("/search")
async def search_assistants():
    return [
        {
            "assistant_id": "lead_agent",
            "graph_id": "lead_agent",
            "config": {},
            "metadata": {},
            "created_at": now_iso(),
            "updated_at": now_iso(),
            "name": "lead_agent",
            "version": 1,
        }
    ]


@router.get("/{assistant_id}/graph")
async def get_assistant_graph(assistant_id: str):
    return {"nodes": [], "edges": []}


@router.get("/{assistant_id}/schemas")
async def get_assistant_schemas(assistant_id: str):
    return {
        "graph_id": assistant_id,
        "state_schema": {},
        "config_schema": {},
    }
