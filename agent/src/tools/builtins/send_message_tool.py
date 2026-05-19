"""Tool for sending messages between teammates in a swarm."""

import asyncio
import logging

from langchain.tools import BaseTool, tool

logger = logging.getLogger(__name__)


@tool("send_message", parse_docstring=True)
def send_message_tool(
    to: str,
    content: str,
) -> str:
    """Send a message to a teammate or broadcast to the entire team.

    Messages are the ONLY way teammates can communicate. Plain text responses
    are NOT visible to other teammates.

    Args:
        to: Recipient teammate name, or "*" to broadcast to all teammates.
        content: The message content to send.
    """
    return "Error: send_message requires team context. This instance is not bound to a team."


def create_send_message_tool(team_id: str, agent_name: str) -> BaseTool:
    """Create a send_message tool bound to a specific team and agent.

    Uses a standalone DB connection so it works inside sync subagent threads
    without touching the main event loop's asyncpg pool.
    """

    @tool("send_message", parse_docstring=True)
    def bound_send_message_tool(
        to: str,
        content: str,
    ) -> str:
        """Send a message to a teammate or broadcast to the entire team.

        Messages are the ONLY way teammates can communicate. Plain text responses
        are NOT visible to other teammates.

        Args:
            to: Recipient teammate name, or "*" to broadcast to all teammates.
            content: The message content to send.
        """
        try:
            asyncio.run(_send_standalone(team_id, agent_name, to, content))
        except Exception as e:
            logger.exception("Failed to send message")
            return f"Error sending message: {e}"

        if to == "*":
            return "Message broadcast to all teammates."
        return f"Message sent to {to}."

    return bound_send_message_tool


async def _send_standalone(team_id: str, from_agent: str, to_agent: str, content: str):
    """Send a message using a standalone DB connection (not the shared pool)."""
    import asyncpg
    from src.storage.database import LANGGRAPH_PG_URI

    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        if to_agent == "*":
            rows = await conn.fetch(
                "SELECT name FROM swarm_team_members WHERE team_id = $1 AND status IN ('active', 'running') AND name != $2",
                team_id, from_agent,
            )
            for row in rows:
                await conn.execute(
                    "INSERT INTO swarm_messages (team_id, from_agent, to_agent, content) VALUES ($1, $2, $3, $4)",
                    team_id, from_agent, row["name"], content,
                )
        else:
            await conn.execute(
                "INSERT INTO swarm_messages (team_id, from_agent, to_agent, content) VALUES ($1, $2, $3, $4)",
                team_id, from_agent, to_agent, content,
            )
    finally:
        await conn.close()
