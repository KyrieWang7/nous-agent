"""Team management for the Swarm system."""

from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import datetime

from src.storage.database import get_db_connection

logger = logging.getLogger(__name__)


@dataclass
class SwarmTeam:
    id: str
    name: str
    description: str | None
    lead_thread_id: str
    created_at: datetime


@dataclass
class SwarmTeamMember:
    id: int
    team_id: str
    name: str
    thread_id: str | None
    model: str | None
    prompt: str | None
    status: str
    joined_at: datetime


class TeamManager:
    """CRUD operations for swarm teams and members."""

    async def create_team(self, name: str, description: str | None, lead_thread_id: str) -> SwarmTeam:
        async with get_db_connection() as conn:
            row = await conn.fetchrow(
                "INSERT INTO swarm_teams (name, description, lead_thread_id) VALUES ($1, $2, $3) RETURNING *",
                name,
                description,
                lead_thread_id,
            )
            team = SwarmTeam(
                id=str(row["id"]),
                name=row["name"],
                description=row["description"],
                lead_thread_id=str(row["lead_thread_id"]),
                created_at=row["created_at"],
            )
            # Register leader as first member
            await self.add_member(team.id, "team-lead", thread_id=lead_thread_id)
            logger.info("[TeamManager] Created team '%s' (id=%s)", name, team.id)
            return team

    async def delete_team(self, team_id: str) -> None:
        async with get_db_connection() as conn:
            await conn.execute("DELETE FROM swarm_teams WHERE id = $1", team_id)
            logger.info("[TeamManager] Deleted team %s", team_id)

    async def add_member(
        self,
        team_id: str,
        name: str,
        *,
        thread_id: str | None = None,
        model: str | None = None,
        prompt: str | None = None,
    ) -> SwarmTeamMember:
        async with get_db_connection() as conn:
            row = await conn.fetchrow(
                """INSERT INTO swarm_team_members (team_id, name, thread_id, model, prompt)
                   VALUES ($1, $2, $3, $4, $5)
                   ON CONFLICT (team_id, name) DO UPDATE
                     SET status = 'running', prompt = EXCLUDED.prompt, joined_at = now()
                   RETURNING *""",
                team_id,
                name,
                thread_id,
                model,
                prompt,
            )
            logger.info("[TeamManager] Added member '%s' to team %s", name, team_id)
            return self._member_from_row(row)

    async def remove_member(self, team_id: str, name: str) -> None:
        async with get_db_connection() as conn:
            await conn.execute(
                "UPDATE swarm_team_members SET status = 'removed' WHERE team_id = $1 AND name = $2",
                team_id,
                name,
            )

    async def get_team(self, name: str) -> SwarmTeam | None:
        async with get_db_connection() as conn:
            row = await conn.fetchrow("SELECT * FROM swarm_teams WHERE name = $1", name)
            if row is None:
                return None
            return SwarmTeam(
                id=str(row["id"]),
                name=row["name"],
                description=row["description"],
                lead_thread_id=str(row["lead_thread_id"]),
                created_at=row["created_at"],
            )

    async def get_team_by_id(self, team_id: str) -> SwarmTeam | None:
        async with get_db_connection() as conn:
            row = await conn.fetchrow("SELECT * FROM swarm_teams WHERE id = $1", team_id)
            if row is None:
                return None
            return SwarmTeam(
                id=str(row["id"]),
                name=row["name"],
                description=row["description"],
                lead_thread_id=str(row["lead_thread_id"]),
                created_at=row["created_at"],
            )

    async def list_members(self, team_id: str, active_only: bool = True) -> list[SwarmTeamMember]:
        async with get_db_connection() as conn:
            if active_only:
                rows = await conn.fetch(
                    "SELECT * FROM swarm_team_members WHERE team_id = $1 AND status = 'active' ORDER BY joined_at",
                    team_id,
                )
            else:
                rows = await conn.fetch(
                    "SELECT * FROM swarm_team_members WHERE team_id = $1 ORDER BY joined_at",
                    team_id,
                )
            return [self._member_from_row(r) for r in rows]

    @staticmethod
    def _member_from_row(row) -> SwarmTeamMember:
        return SwarmTeamMember(
            id=row["id"],
            team_id=str(row["team_id"]),
            name=row["name"],
            thread_id=str(row["thread_id"]) if row["thread_id"] else None,
            model=row["model"],
            prompt=row["prompt"],
            status=row["status"],
            joined_at=row["joined_at"],
        )
