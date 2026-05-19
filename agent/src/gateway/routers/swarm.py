"""Swarm/Team API router — REST queries + SSE stream."""

import asyncio
import json
import logging
from datetime import datetime, timezone

from fastapi import APIRouter, Query
from fastapi.responses import StreamingResponse

from src.swarm.mailbox import PostgresMailbox
from src.swarm.team import TeamManager

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api/swarm", tags=["swarm"])

_team_manager = TeamManager()
_mailbox = PostgresMailbox()


@router.get("/teams")
async def get_teams(thread_id: str = Query(..., description="Lead thread ID")):
    """Get teams associated with a thread."""
    import uuid as _uuid

    from src.storage.database import get_db_connection

    try:
        tid = _uuid.UUID(thread_id)
    except ValueError:
        return {"teams": []}

    async with get_db_connection() as conn:
        rows = await conn.fetch(
            "SELECT id, name, description, created_at FROM swarm_teams WHERE lead_thread_id = $1 ORDER BY created_at DESC",
            tid,
        )
    return {
        "teams": [
            {
                "id": str(r["id"]),
                "name": r["name"],
                "description": r["description"],
                "created_at": r["created_at"].isoformat(),
            }
            for r in rows
        ]
    }


@router.get("/teams/{team_id}/members")
async def get_team_members(team_id: str):
    """Get members of a team."""
    members = await _team_manager.list_members(team_id, active_only=False)
    return {
        "members": [
            {
                "name": m.name,
                "status": m.status,
                "model": m.model,
                "joined_at": m.joined_at.isoformat() if m.joined_at else None,
            }
            for m in members
        ]
    }


@router.get("/teams/{team_id}/stream")
async def stream_team_events(team_id: str):
    """SSE stream of team events (messages, member changes, heartbeat)."""
    from src.config.swarm_config import get_swarm_config

    swarm_cfg = get_swarm_config()
    poll_interval = swarm_cfg.message_poll_interval_seconds

    async def event_generator():
        try:
            members = await _team_manager.list_members(team_id, active_only=False)
            members_data = [
                {"name": m.name, "status": m.status, "model": m.model, "joined_at": m.joined_at.isoformat() if m.joined_at else None}
                for m in members
            ]
            yield f"event: team_update\ndata: {json.dumps(members_data)}\n\n"

            recent = await _mailbox.get_last_n_messages(team_id, n=50)
            for msg in recent:
                msg_data = {"id": msg.id, "from": msg.from_agent, "to": msg.to_agent, "content": msg.content, "created_at": msg.created_at.isoformat()}
                yield f"id: {msg.id}\nevent: message\ndata: {json.dumps(msg_data)}\n\n"
        except Exception as e:
            yield f"event: error\ndata: {json.dumps({'error': str(e)})}\n\n"
            return

        last_poll = datetime.now(timezone.utc)
        last_member_snapshot = {m.name: m.status for m in members}
        heartbeat_counter = 0.0

        while True:
            try:
                await asyncio.sleep(poll_interval)
                now = datetime.now(timezone.utc)

                team = await _team_manager.get_team_by_id(team_id)
                if team is None:
                    yield f"event: team_deleted\ndata: {json.dumps({'team_id': team_id})}\n\n"
                    return

                new_messages = await _mailbox.get_recent_messages(team_id, since=last_poll)
                for msg in new_messages:
                    msg_data = {"id": msg.id, "from": msg.from_agent, "to": msg.to_agent, "content": msg.content, "created_at": msg.created_at.isoformat()}
                    yield f"id: {msg.id}\nevent: message\ndata: {json.dumps(msg_data)}\n\n"

                    if msg.from_agent == "system":
                        for prefix, status in [("[Joined]", "active"), ("[Completed]", "completed"), ("[Failed]", "failed"), ("[Timeout]", "timeout")]:
                            if msg.content.startswith(prefix):
                                agent_name = msg.content[len(prefix):].split(":")[0].strip()
                                yield f"event: status\ndata: {json.dumps({'agent_name': agent_name, 'status': status, 'detail': msg.content})}\n\n"
                                break

                last_poll = now

                current_members = await _team_manager.list_members(team_id, active_only=False)
                current_snapshot = {m.name: m.status for m in current_members}
                if current_snapshot != last_member_snapshot:
                    members_data = [
                        {"name": m.name, "status": m.status, "model": m.model, "joined_at": m.joined_at.isoformat() if m.joined_at else None}
                        for m in current_members
                    ]
                    yield f"event: team_update\ndata: {json.dumps(members_data)}\n\n"
                    last_member_snapshot = current_snapshot

                heartbeat_counter += poll_interval
                if heartbeat_counter >= 30:
                    yield f"event: heartbeat\ndata: {json.dumps({'timestamp': now.isoformat()})}\n\n"
                    heartbeat_counter = 0.0

            except asyncio.CancelledError:
                return
            except Exception as e:
                logger.error("SSE stream error for team %s: %s", team_id, e)
                yield f"event: error\ndata: {json.dumps({'error': str(e)})}\n\n"
                await asyncio.sleep(5)

    return StreamingResponse(
        event_generator(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "Connection": "keep-alive", "X-Accel-Buffering": "no"},
    )
