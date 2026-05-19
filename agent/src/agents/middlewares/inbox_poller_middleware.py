"""InboxPollerMiddleware - polls teammate mailbox before each model call."""

import asyncio
import logging
from typing import override

from langchain.agents import AgentState
from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import HumanMessage
from langgraph.runtime import Runtime

logger = logging.getLogger(__name__)


class InboxPollerMiddleware(AgentMiddleware[AgentState]):
    """Poll the teammate inbox before each model call and inject unread messages.

    When swarm mode is active and the current session has a swarm context,
    this middleware checks for unread messages from teammates and injects
    them as HumanMessages so the LLM can see and respond to them.
    """

    @override
    def before_model(self, state: AgentState, runtime: Runtime) -> dict | None:
        return self._poll(state, runtime)

    @override
    async def abefore_model(self, state: AgentState, runtime: Runtime) -> dict | None:
        return self._poll(state, runtime)

    def _poll(self, state: AgentState, runtime: Runtime) -> dict | None:
        swarm_ctx = state.get("swarm_context")
        if not swarm_ctx:
            return None

        team_id = swarm_ctx.get("team_id")
        agent_name = swarm_ctx.get("agent_name")
        if not team_id or not agent_name:
            return None

        try:
            from src.swarm.mailbox import PostgresMailbox

            mailbox = PostgresMailbox()
            messages = asyncio.get_event_loop().run_until_complete(
                mailbox.poll_inbox(team_id, agent_name)
            )

            if not messages:
                return None

            injected = []
            for msg in messages:
                injected.append(
                    HumanMessage(
                        content=f"[Teammate Message from {msg.from_agent}]: {msg.content}"
                    )
                )

            logger.info(
                "[InboxPoller] Injected %d message(s) for %s in team %s",
                len(injected),
                agent_name,
                team_id,
            )
            return {"messages": injected}

        except Exception:
            logger.exception("InboxPoller failed to poll mailbox")
            return None
