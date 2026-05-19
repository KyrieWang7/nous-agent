"""TeammateSpawner — spawn named, addressable teammates via SubagentExecutor."""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass

from src.swarm.constants import TEAMMATE_PROMPT_ADDENDUM
from src.swarm.mailbox import PostgresMailbox
from src.swarm.team import TeamManager

logger = logging.getLogger(__name__)


@dataclass
class SpawnTeammateConfig:
    name: str
    prompt: str
    team_id: str
    team_name: str
    model: str | None = None
    subagent_type: str = "general-purpose"
    timeout_seconds: int = 900


@dataclass
class SpawnResult:
    teammate_name: str
    team_id: str
    status: str
    result: str | None = None
    error: str | None = None


class TeammateSpawner:
    """Spawns teammate agents as in-process subagents with team awareness."""

    def __init__(self) -> None:
        self._team_manager = TeamManager()
        self._mailbox = PostgresMailbox()

    async def spawn(
        self,
        config: SpawnTeammateConfig,
        *,
        parent_model: str | None = None,
        thread_id: str | None = None,
    ) -> SpawnResult:
        """Spawn a teammate and register it in the team.

        The teammate runs as a subagent with an enhanced system prompt that
        includes team communication rules. Upon completion, a notification
        is sent to the team-lead via the mailbox.
        """
        from src.subagents.executor import SubagentExecutor
        from src.subagents.registry import get_subagent_config
        from src.tools import get_available_tools

        # Register member
        await self._team_manager.add_member(
            config.team_id,
            config.name,
            model=config.model,
            prompt=config.prompt,
        )

        await self._mailbox.send_message(
            team_id=config.team_id,
            from_agent="system",
            to_agent="*",
            content=f"[Joined] {config.name} started working on: {config.prompt[:100]}",
        )

        # Build subagent config
        base_config = get_subagent_config(config.subagent_type)
        if base_config is None:
            base_config = get_subagent_config("general-purpose")

        enhanced_prompt = f"{base_config.system_prompt}\n\n{TEAMMATE_PROMPT_ADDENDUM}\n\nYour name in this team is: {config.name}\nTeam: {config.team_name}\n\nYour task:\n{config.prompt}"

        from src.subagents.config import SubagentConfig

        teammate_config = SubagentConfig(
            name=config.name,
            description=f"Teammate {config.name}",
            system_prompt=enhanced_prompt,
            model=base_config.model,
            tools=base_config.tools,
            disallowed_tools=base_config.disallowed_tools,
            max_turns=base_config.max_turns,
            timeout_seconds=config.timeout_seconds,
        )

        tools = get_available_tools(subagent_enabled=False)

        # Add send_message tool for teammate communication
        try:
            from src.tools.builtins.send_message_tool import create_send_message_tool

            send_msg_tool = create_send_message_tool(config.team_id, config.name)
            tools.append(send_msg_tool)
        except ImportError:
            logger.warning("send_message_tool not available for teammate %s", config.name)

        executor = SubagentExecutor(
            config=teammate_config,
            tools=tools,
            parent_model=parent_model,
            thread_id=thread_id,
        )

        try:
            subagent_result = await asyncio.to_thread(
                executor.execute,
                config.prompt,
            )
            result_text = subagent_result.result or ""

            await self._mailbox.send_message(
                team_id=config.team_id,
                from_agent="system",
                to_agent="*",
                content=f"[Completed] {config.name}: {result_text[:200] if result_text else 'done'}",
            )

            # Notify leader via mailbox
            await self._mailbox.send_message(
                team_id=config.team_id,
                from_agent=config.name,
                to_agent="team-lead",
                content=f"[Task Complete] {result_text}",
            )

            await self._team_manager.remove_member(config.team_id, config.name)

            return SpawnResult(
                teammate_name=config.name,
                team_id=config.team_id,
                status="completed",
                result=result_text,
            )

        except Exception as e:
            logger.exception("Teammate %s failed", config.name)

            await self._mailbox.send_message(
                team_id=config.team_id,
                from_agent="system",
                to_agent="*",
                content=f"[Failed] {config.name}: {e}",
            )

            await self._mailbox.send_message(
                team_id=config.team_id,
                from_agent=config.name,
                to_agent="team-lead",
                content=f"[Task Failed] {e}",
            )

            await self._team_manager.remove_member(config.team_id, config.name)

            return SpawnResult(
                teammate_name=config.name,
                team_id=config.team_id,
                status="failed",
                error=str(e),
            )
