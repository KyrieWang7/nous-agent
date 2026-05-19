"""Tool for deleting a swarm team."""

import asyncio
import logging

import asyncpg
from langchain.tools import tool

from src.storage.database import LANGGRAPH_PG_URI

logger = logging.getLogger(__name__)


@tool("team_delete", parse_docstring=True)
def team_delete_tool(
    team_name: str,
) -> str:
    """Delete a team and all its members/messages.

    This permanently removes the team, all member records, and all messages.
    Only use when the team's work is fully complete.

    Args:
        team_name: The name of the team to delete.
    """
    try:
        asyncio.run(_delete_team_standalone(team_name))
    except Exception as e:
        logger.exception("Failed to delete team")
        return f"Error deleting team: {e}"

    return f"Team '{team_name}' deleted successfully."


async def _delete_team_standalone(team_name: str):
    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        row = await conn.fetchrow("SELECT id FROM swarm_teams WHERE name = $1", team_name)
        if row is None:
            raise ValueError(f"Team '{team_name}' not found.")
        await conn.execute("DELETE FROM swarm_teams WHERE id = $1", row["id"])
        logger.info("[team_delete] Deleted team '%s'", team_name)
    finally:
        await conn.close()
