"""Gateway HTTP client - access Gateway API from Backend."""

import logging
from typing import Any

import httpx

logger = logging.getLogger(__name__)

_client: httpx.Client | None = None
_base_url: str = "http://localhost:7777"


def configure(base_url: str = "http://localhost:7777"):
    """Configure the gateway client."""
    global _base_url, _client
    _base_url = base_url
    if _client is not None:
        _client.close()
        _client = None


def _get_client() -> httpx.Client:
    global _client
    if _client is None:
        _client = httpx.Client(base_url=_base_url, timeout=30)
    return _client


# ── Memory API ──────────────────────────────────────

def get_memory(user_id: str, token: str) -> dict[str, Any]:
    """Fetch user memory in legacy format for agent injection."""
    client = _get_client()
    resp = client.get(
        "/api/memory/legacy",
        headers={"Authorization": f"Bearer {token}"},
    )
    resp.raise_for_status()
    return resp.json()


def update_memory(
    user_id: str,
    token: str,
    sections: list[dict] | None = None,
    new_facts: list[dict] | None = None,
    remove_fact_ids: list[str] | None = None,
) -> dict[str, Any]:
    """Bulk update user memory."""
    client = _get_client()
    resp = client.post(
        "/api/memory/bulk-update",
        headers={"Authorization": f"Bearer {token}"},
        json={
            "sections": sections or [],
            "new_facts": new_facts or [],
            "remove_fact_ids": remove_fact_ids or [],
        },
    )
    resp.raise_for_status()
    return resp.json()


# ── Thread Messages API ──────────────────────────────

def save_messages(thread_id: str, token: str, messages: list[dict]) -> None:
    """Save messages to a thread via Gateway API."""
    client = _get_client()
    for msg in messages:
        resp = client.post(
            f"/api/threads/{thread_id}/messages",
            headers={"Authorization": f"Bearer {token}"},
            json=msg,
        )
        resp.raise_for_status()


def get_thread_messages(thread_id: str, token: str) -> list[dict]:
    """Fetch all messages from a thread."""
    client = _get_client()
    resp = client.get(
        f"/api/threads/{thread_id}/messages",
        headers={"Authorization": f"Bearer {token}"},
    )
    resp.raise_for_status()
    return resp.json()


# ── Models API ──────────────────────────────────

def list_models() -> list[dict]:
    """List available models (no auth required)."""
    client = _get_client()
    resp = client.get("/api/models")
    resp.raise_for_status()
    return resp.json().get("models", [])
