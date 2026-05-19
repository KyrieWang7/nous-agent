"""Tool for creating a new swarm team."""

import asyncio
import logging

import asyncpg
from langchain.tools import ToolRuntime, tool
from langgraph.typing import ContextT

from src.agents.thread_state import ThreadState
from src.storage.database import LANGGRAPH_PG_URI

logger = logging.getLogger(__name__)


@tool("team_create", parse_docstring=True)
def team_create_tool(
    runtime: ToolRuntime[ContextT, ThreadState],
    name: str,
    description: str = "",
) -> str:
    """Create a new team for multi-agent collaboration.

    Creates a team where you (the leader) can spawn named teammates that
    communicate via the send_message tool. Use teams when a task benefits
    from parallel work by specialized agents.

    Args:
        name: A unique name for the team (e.g. "code-review", "refactor-auth").
        description: Optional description of the team's purpose.
    """
    thread_id = None
    if runtime is not None:
        thread_id = runtime.context.get("thread_id")

    if not thread_id:
        return "Error: Cannot create team — no thread_id in runtime context."

    try:
        team_id = asyncio.run(_create_team_standalone(name, description or None, thread_id))
    except Exception as e:
        if "unique" in str(e).lower() or "duplicate" in str(e).lower():
            return f"Error: Team '{name}' already exists. Use a different name or delete it first."
        logger.exception("Failed to create team")
        return f"Error creating team: {e}"

    return f"Team '{name}' created (id={team_id}). You are the team-lead. Use task tool with name parameter to spawn teammates, or send_message to communicate."


async def _create_team_standalone(name: str, description: str | None, lead_thread_id: str) -> str:
    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        row = await conn.fetchrow(
            "INSERT INTO swarm_teams (name, description, lead_thread_id) VALUES ($1, $2, $3) RETURNING id",
            name, description, lead_thread_id,
        )
        team_id = str(row["id"])
        await conn.execute(
            """INSERT INTO swarm_team_members (team_id, name, thread_id)
               VALUES ($1, $2, $3)
               ON CONFLICT (team_id, name) DO UPDATE SET status = 'active'""",
            team_id, "team-lead", lead_thread_id,
        )
        logger.info("[team_create] Created team '%s' (id=%s)", name, team_id)
        return team_id
    finally:
        await conn.close()
