"""Tool for listing teammates in a swarm team."""

import asyncio
import logging

import asyncpg
from langchain.tools import tool

from src.storage.database import LANGGRAPH_PG_URI

logger = logging.getLogger(__name__)


@tool("list_teammates", parse_docstring=True)
def list_teammates_tool(
    team_name: str,
) -> str:
    """List all active teammates in a team.

    Shows each teammate's name, status, and model. Use this to check
    who is available for communication via send_message.

    Args:
        team_name: The name of the team to list members for.
    """
    try:
        result = asyncio.run(_list_standalone(team_name))
    except Exception as e:
        logger.exception("Failed to list teammates")
        return f"Error listing teammates: {e}"

    return result


async def _list_standalone(team_name: str) -> str:
    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        row = await conn.fetchrow("SELECT id FROM swarm_teams WHERE name = $1", team_name)
        if row is None:
            return f"Error: Team '{team_name}' not found."
        team_id = str(row["id"])

        members = await conn.fetch(
            "SELECT name, status, model FROM swarm_team_members WHERE team_id = $1 AND status IN ('active', 'running') ORDER BY joined_at",
            team_id,
        )
    finally:
        await conn.close()

    if not members:
        return f"Team '{team_name}' has no active members."

    lines = [f"Team '{team_name}' ({len(members)} members):"]
    for m in members:
        model_info = f" (model: {m['model']})" if m["model"] else ""
        lines.append(f"  - {m['name']} [{m['status']}]{model_info}")
    return "\n".join(lines)
