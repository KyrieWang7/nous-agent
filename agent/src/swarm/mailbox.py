"""PostgreSQL-backed mailbox for teammate messaging."""

from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import datetime

from src.storage.database import get_db_connection

logger = logging.getLogger(__name__)


@dataclass
class SwarmMessage:
    id: int
    team_id: str
    from_agent: str
    to_agent: str
    content: str
    read: bool
    created_at: datetime


class PostgresMailbox:
    """Mailbox service backed by the swarm_messages table."""

    async def send_message(
        self,
        team_id: str,
        from_agent: str,
        to_agent: str,
        content: str,
    ) -> None:
        """Send a message. If to_agent is '*', broadcast to all active members."""
        async with get_db_connection() as conn:
            if to_agent == "*":
                rows = await conn.fetch(
                    "SELECT name FROM swarm_team_members WHERE team_id = $1 AND status IN ('active', 'running') AND name != $2",
                    team_id,
                    from_agent,
                )
                for row in rows:
                    await conn.execute(
                        "INSERT INTO swarm_messages (team_id, from_agent, to_agent, content) VALUES ($1, $2, $3, $4)",
                        team_id,
                        from_agent,
                        row["name"],
                        content,
                    )
                logger.info("[Mailbox] Broadcast from %s to %d members in team %s", from_agent, len(rows), team_id)
            else:
                await conn.execute(
                    "INSERT INTO swarm_messages (team_id, from_agent, to_agent, content) VALUES ($1, $2, $3, $4)",
                    team_id,
                    from_agent,
                    to_agent,
                    content,
                )
                logger.info("[Mailbox] Message from %s to %s in team %s", from_agent, to_agent, team_id)

    async def poll_inbox(self, team_id: str, agent_name: str) -> list[SwarmMessage]:
        """Read unread messages for an agent and mark them as read."""
        async with get_db_connection() as conn:
            rows = await conn.fetch(
                """
                UPDATE swarm_messages
                SET read = TRUE
                WHERE team_id = $1 AND to_agent = $2 AND read = FALSE
                RETURNING id, team_id, from_agent, to_agent, content, read, created_at
                """,
                team_id,
                agent_name,
            )
            return [
                SwarmMessage(
                    id=r["id"],
                    team_id=str(r["team_id"]),
                    from_agent=r["from_agent"],
                    to_agent=r["to_agent"],
                    content=r["content"],
                    read=r["read"],
                    created_at=r["created_at"],
                )
                for r in rows
            ]

    async def get_unread_count(self, team_id: str, agent_name: str) -> int:
        async with get_db_connection() as conn:
            row = await conn.fetchrow(
                "SELECT COUNT(*) as cnt FROM swarm_messages WHERE team_id = $1 AND to_agent = $2 AND read = FALSE",
                team_id,
                agent_name,
            )
            return row["cnt"] if row else 0

    async def get_recent_messages(self, team_id: str, since: datetime) -> list[SwarmMessage]:
        """Get all messages after a given timestamp (global view, does not mark as read)."""
        async with get_db_connection() as conn:
            rows = await conn.fetch(
                """SELECT id, team_id, from_agent, to_agent, content, read, created_at
                   FROM swarm_messages
                   WHERE team_id = $1 AND created_at > $2
                   ORDER BY created_at ASC""",
                team_id,
                since,
            )
            return [
                SwarmMessage(
                    id=r["id"],
                    team_id=str(r["team_id"]),
                    from_agent=r["from_agent"],
                    to_agent=r["to_agent"],
                    content=r["content"],
                    read=r["read"],
                    created_at=r["created_at"],
                )
                for r in rows
            ]

    async def get_last_n_messages(self, team_id: str, n: int = 50) -> list[SwarmMessage]:
        """Get the last N messages for initial SSE connection backfill."""
        async with get_db_connection() as conn:
            rows = await conn.fetch(
                """SELECT id, team_id, from_agent, to_agent, content, read, created_at
                   FROM swarm_messages
                   WHERE team_id = $1
                   ORDER BY created_at DESC
                   LIMIT $2""",
                team_id,
                n,
            )
            rows = list(reversed(rows))
            return [
                SwarmMessage(
                    id=r["id"],
                    team_id=str(r["team_id"]),
                    from_agent=r["from_agent"],
                    to_agent=r["to_agent"],
                    content=r["content"],
                    read=r["read"],
                    created_at=r["created_at"],
                )
                for r in rows
            ]
