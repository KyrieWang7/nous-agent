"""Task tool for delegating work to subagents."""

import logging
import time
import uuid
from dataclasses import replace
from typing import Annotated, Literal

from langchain.tools import InjectedToolCallId, ToolRuntime, tool
from langgraph.config import get_stream_writer
from langgraph.typing import ContextT

from src.agents.lead_agent.prompt import get_skills_prompt_section
from src.agents.thread_state import ThreadState
from src.subagents import SubagentExecutor, get_subagent_config
from src.subagents.executor import SubagentStatus, get_background_task_result

logger = logging.getLogger(__name__)


@tool("task", parse_docstring=True)
def task_tool(
    runtime: ToolRuntime[ContextT, ThreadState],
    description: str,
    prompt: str,
    subagent_type: Literal["general-purpose", "bash", "explore", "plan", "verification"],
    tool_call_id: Annotated[str, InjectedToolCallId],
    max_turns: int | None = None,
    name: str | None = None,
    team_name: str | None = None,
) -> str:
    """Delegate a task to a specialized subagent that runs in its own context.

    Subagents help you:
    - Preserve context by keeping exploration and implementation separate
    - Handle complex multi-step tasks autonomously
    - Execute commands or operations in isolated contexts

    Available subagent types:
    - **general-purpose**: A capable agent for complex, multi-step tasks that require
      both exploration and action. Use when the task requires complex reasoning,
      multiple dependent steps, or would benefit from isolated context.
    - **bash**: Command execution specialist for running bash commands. Use for
      git operations, build processes, or when command output would be verbose.
    - **explore**: Read-only agent for codebase exploration (Glob, Grep, Read).
    - **plan**: Read-only agent for structured planning output.
    - **verification**: Agent that can run bash for testing and verification.

    When swarm mode is enabled, you can spawn named teammates:
    - Set `name` to make the agent addressable via send_message
    - Set `team_name` to assign the agent to a team

    Args:
        description: A short (3-5 word) description of the task for logging/display. ALWAYS PROVIDE THIS PARAMETER FIRST.
        prompt: The task description for the subagent. Be specific and clear about what needs to be done. ALWAYS PROVIDE THIS PARAMETER SECOND.
        subagent_type: The type of subagent to use. ALWAYS PROVIDE THIS PARAMETER THIRD.
        max_turns: Optional maximum number of agent turns. Defaults to subagent's configured max.
        name: Optional name for the agent (swarm mode). Makes it addressable via send_message.
        team_name: Optional team name (swarm mode). Assigns the agent to an existing team.
    """
    # Resolve team context: if swarm is on and a team exists, auto-bind
    resolved_team = team_name
    resolved_name = name
    if runtime is not None and not resolved_team:
        swarm_team = runtime.config.get("configurable", {}).get("swarm_team_name")
        if swarm_team:
            resolved_team = swarm_team
            if not resolved_name:
                short_desc = description.replace(" ", "-")[:20] if description else subagent_type
                resolved_name = f"{short_desc}-{uuid.uuid4().hex[:6]}"

    if resolved_name and resolved_team:
        return _spawn_as_teammate(runtime, description, prompt, subagent_type, resolved_name, resolved_team, tool_call_id)
    # Get subagent configuration
    config = get_subagent_config(subagent_type)
    if config is None:
        return f"Error: Unknown subagent type '{subagent_type}'. Available: general-purpose, bash"

    # Build config overrides
    overrides: dict = {}

    skills_section = get_skills_prompt_section()
    if skills_section:
        overrides["system_prompt"] = config.system_prompt + "\n\n" + skills_section

    if max_turns is not None:
        overrides["max_turns"] = max_turns

    if overrides:
        config = replace(config, **overrides)

    # Extract parent context from runtime
    sandbox_state = None
    thread_data = None
    thread_id = None
    parent_model = None
    trace_id = None

    if runtime is not None:
        sandbox_state = runtime.state.get("sandbox")
        thread_data = runtime.state.get("thread_data")
        thread_id = runtime.context.get("thread_id")

        # Try to get parent model from configurable
        metadata = runtime.config.get("metadata", {})
        parent_model = metadata.get("model_name")

        # Get or generate trace_id for distributed tracing
        trace_id = metadata.get("trace_id") or str(uuid.uuid4())[:8]

    # Get available tools (excluding task tool to prevent nesting)
    # Lazy import to avoid circular dependency
    from src.tools import get_available_tools

    # Subagents should not have subagent tools enabled (prevent recursive nesting)
    tools = get_available_tools(model_name=parent_model, subagent_enabled=False)

    # Create executor
    executor = SubagentExecutor(
        config=config,
        tools=tools,
        parent_model=parent_model,
        sandbox_state=sandbox_state,
        thread_data=thread_data,
        thread_id=thread_id,
        trace_id=trace_id,
    )

    # Start background execution (always async to prevent blocking)
    # Use tool_call_id as task_id for better traceability
    task_id = executor.execute_async(prompt, task_id=tool_call_id)

    # Poll for task completion in backend (removes need for LLM to poll)
    poll_count = 0
    last_status = None
    last_message_count = 0  # Track how many AI messages we've already sent
    # Polling timeout: execution timeout + 60s buffer, checked every 5s
    max_poll_count = (config.timeout_seconds + 60) // 5

    logger.info(f"[trace={trace_id}] Started background task {task_id} (subagent={subagent_type}, timeout={config.timeout_seconds}s, polling_limit={max_poll_count} polls)")

    writer = get_stream_writer()
    # Send Task Started message'
    writer({"type": "task_started", "task_id": task_id, "description": description})

    while True:
        result = get_background_task_result(task_id)

        if result is None:
            logger.error(f"[trace={trace_id}] Task {task_id} not found in background tasks")
            writer({"type": "task_failed", "task_id": task_id, "error": "Task disappeared from background tasks"})
            return f"Error: Task {task_id} disappeared from background tasks"

        # Log status changes for debugging
        if result.status != last_status:
            logger.info(f"[trace={trace_id}] Task {task_id} status: {result.status.value}")
            last_status = result.status

        # Check for new AI messages and send task_running events
        current_message_count = len(result.ai_messages)
        if current_message_count > last_message_count:
            # Send task_running event for each new message
            for i in range(last_message_count, current_message_count):
                message = result.ai_messages[i]
                writer(
                    {
                        "type": "task_running",
                        "task_id": task_id,
                        "message": message,
                        "message_index": i + 1,  # 1-based index for display
                        "total_messages": current_message_count,
                    }
                )
                logger.info(f"[trace={trace_id}] Task {task_id} sent message #{i + 1}/{current_message_count}")
            last_message_count = current_message_count

        # Check if task completed, failed, or timed out
        if result.status == SubagentStatus.COMPLETED:
            writer({"type": "task_completed", "task_id": task_id, "result": result.result})
            logger.info(f"[trace={trace_id}] Task {task_id} completed after {poll_count} polls")
            return f"Task Succeeded. Result: {result.result}"
        elif result.status == SubagentStatus.FAILED:
            writer({"type": "task_failed", "task_id": task_id, "error": result.error})
            logger.error(f"[trace={trace_id}] Task {task_id} failed: {result.error}")
            return f"Task failed. Error: {result.error}"
        elif result.status == SubagentStatus.TIMED_OUT:
            writer({"type": "task_timed_out", "task_id": task_id, "error": result.error})
            logger.warning(f"[trace={trace_id}] Task {task_id} timed out: {result.error}")
            return f"Task timed out. Error: {result.error}"

        # Still running, wait before next poll
        time.sleep(5)  # Poll every 5 seconds
        poll_count += 1

        # Polling timeout as a safety net (in case thread pool timeout doesn't work)
        # Set to execution timeout + 60s buffer, in 5s poll intervals
        # This catches edge cases where the background task gets stuck
        if poll_count > max_poll_count:
            timeout_minutes = config.timeout_seconds // 60
            logger.error(f"[trace={trace_id}] Task {task_id} polling timed out after {poll_count} polls (should have been caught by thread pool timeout)")
            writer({"type": "task_timed_out", "task_id": task_id})
            return f"Task polling timed out after {timeout_minutes} minutes. This may indicate the background task is stuck. Status: {result.status.value}"


def _swarm_db_op(coro):
    """Run an async DB operation using a standalone connection in a fresh loop.

    This avoids deadlocks because it does NOT touch the main event loop
    or the shared asyncpg pool (which is bound to the main loop).
    """
    import asyncio
    return asyncio.run(coro)


async def _get_team_standalone(team_name: str):
    """Look up a team by name using a standalone DB connection."""
    import asyncpg
    from src.storage.database import LANGGRAPH_PG_URI
    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        row = await conn.fetchrow("SELECT * FROM swarm_teams WHERE name = $1", team_name)
        if row is None:
            return None
        from src.swarm.team import SwarmTeam
        return SwarmTeam(
            id=str(row["id"]),
            name=row["name"],
            description=row["description"],
            lead_thread_id=str(row["lead_thread_id"]),
            created_at=row["created_at"],
        )
    finally:
        await conn.close()


async def _register_teammate(team_id: str, name: str, prompt: str):
    """Register a teammate and broadcast [Joined] using standalone connection."""
    import asyncpg
    from src.storage.database import LANGGRAPH_PG_URI
    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        await conn.execute(
            """INSERT INTO swarm_team_members (team_id, name, prompt)
               VALUES ($1, $2, $3)
               ON CONFLICT (team_id, name) DO UPDATE
                 SET status = 'running', prompt = EXCLUDED.prompt, joined_at = now()""",
            team_id, name, prompt,
        )
        joined_msg = f"[Joined] {name} started working on: {prompt[:100]}"
        recipients = await conn.fetch(
            "SELECT name FROM swarm_team_members WHERE team_id = $1 AND status IN ('active', 'running') AND name != $2",
            team_id, "system",
        )
        for r in recipients:
            await conn.execute(
                "INSERT INTO swarm_messages (team_id, from_agent, to_agent, content) VALUES ($1, $2, $3, $4)",
                team_id, "system", r["name"], joined_msg,
            )
    finally:
        await conn.close()


async def _finalize_teammate(team_id: str, name: str, status: str, result_text: str):
    """Broadcast completion/failure and remove member using standalone connection."""
    import asyncpg
    from src.storage.database import LANGGRAPH_PG_URI
    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        prefix = "[Completed]" if status == "completed" else "[Failed]"
        summary = result_text[:200] if result_text else "done"
        broadcast_msg = f"{prefix} {name}: {summary}"
        recipients = await conn.fetch(
            "SELECT name FROM swarm_team_members WHERE team_id = $1 AND status IN ('active', 'running') AND name != $2",
            team_id, "system",
        )
        for r in recipients:
            await conn.execute(
                "INSERT INTO swarm_messages (team_id, from_agent, to_agent, content) VALUES ($1, $2, $3, $4)",
                team_id, "system", r["name"], broadcast_msg,
            )
        await conn.execute(
            "INSERT INTO swarm_messages (team_id, from_agent, to_agent, content) VALUES ($1, $2, $3, $4)",
            team_id, name, "team-lead", f"[Task {'Complete' if status == 'completed' else 'Failed'}] {result_text}",
        )
        await conn.execute(
            "UPDATE swarm_team_members SET status = 'removed' WHERE team_id = $1 AND name = $2",
            team_id, name,
        )
    finally:
        await conn.close()


def _spawn_as_teammate(
    runtime: ToolRuntime,
    description: str,
    prompt: str,
    subagent_type: str,
    name: str,
    team_name: str,
    tool_call_id: str,
) -> str:
    """Spawn a task as a named teammate in a swarm team.

    Uses the same execute_async + poll pattern as regular subagents to
    avoid deadlocking the main event loop.  All DB operations use
    standalone connections (not the shared pool).
    """
    from src.subagents.config import SubagentConfig
    from src.swarm.constants import TEAMMATE_PROMPT_ADDENDUM

    team = _swarm_db_op(_get_team_standalone(team_name))
    if team is None:
        return f"Error: Team '{team_name}' not found. Create it first with team_create."

    thread_id = None
    parent_model = None
    trace_id = str(uuid.uuid4())[:8]
    if runtime is not None:
        thread_id = runtime.context.get("thread_id")
        parent_model = runtime.config.get("metadata", {}).get("model_name")
        trace_id = runtime.config.get("metadata", {}).get("trace_id") or trace_id

    timeout = 900
    try:
        from src.config.swarm_config import get_swarm_config
        timeout = get_swarm_config().teammate_timeout_seconds
    except Exception:
        pass

    # Register teammate + broadcast [Joined]
    _swarm_db_op(_register_teammate(team.id, name, prompt))

    # Build subagent config (same as spawner but inline)
    base_config = get_subagent_config(subagent_type) or get_subagent_config("general-purpose")
    enhanced_prompt = (
        f"{base_config.system_prompt}\n\n{TEAMMATE_PROMPT_ADDENDUM}\n\n"
        f"Your name in this team is: {name}\nTeam: {team_name}\n\nYour task:\n{prompt}"
    )
    teammate_config = SubagentConfig(
        name=name,
        description=f"Teammate {name}",
        system_prompt=enhanced_prompt,
        model=base_config.model,
        tools=base_config.tools,
        disallowed_tools=base_config.disallowed_tools,
        max_turns=base_config.max_turns,
        timeout_seconds=timeout,
    )

    from src.tools import get_available_tools
    tools = get_available_tools(model_name=parent_model, subagent_enabled=False)

    try:
        from src.tools.builtins.send_message_tool import create_send_message_tool
        tools.append(create_send_message_tool(team.id, name))
    except ImportError:
        pass

    executor = SubagentExecutor(
        config=teammate_config,
        tools=tools,
        parent_model=parent_model,
        thread_id=thread_id,
        trace_id=trace_id,
    )

    # Async background execution + polling (same as non-swarm path)
    task_id = executor.execute_async(prompt, task_id=tool_call_id)
    max_poll_count = (timeout + 60) // 5
    poll_count = 0
    last_status = None
    last_message_count = 0

    logger.info("[trace=%s] Teammate %s started (team=%s, task=%s)", trace_id, name, team_name, task_id)

    writer = get_stream_writer()
    writer({"type": "task_started", "task_id": task_id, "description": f"[Teammate:{name}] {description}"})

    while True:
        result = get_background_task_result(task_id)
        if result is None:
            _swarm_db_op(_finalize_teammate(team.id, name, "failed", "Task disappeared"))
            writer({"type": "task_failed", "task_id": task_id, "error": "Task disappeared"})
            return f"Teammate '{name}' failed: task disappeared"

        if result.status != last_status:
            logger.info("[trace=%s] Teammate %s status: %s", trace_id, name, result.status.value)
            last_status = result.status

        current_message_count = len(result.ai_messages)
        if current_message_count > last_message_count:
            for i in range(last_message_count, current_message_count):
                writer({
                    "type": "task_running", "task_id": task_id,
                    "message": result.ai_messages[i],
                    "message_index": i + 1, "total_messages": current_message_count,
                })
            last_message_count = current_message_count

        if result.status == SubagentStatus.COMPLETED:
            result_text = result.result or ""
            _swarm_db_op(_finalize_teammate(team.id, name, "completed", result_text))
            writer({"type": "task_completed", "task_id": task_id, "result": result_text})
            return f"Teammate '{name}' completed. Result: {result_text}"
        elif result.status == SubagentStatus.FAILED:
            _swarm_db_op(_finalize_teammate(team.id, name, "failed", result.error or ""))
            writer({"type": "task_failed", "task_id": task_id, "error": result.error})
            return f"Teammate '{name}' failed: {result.error}"
        elif result.status == SubagentStatus.TIMED_OUT:
            _swarm_db_op(_finalize_teammate(team.id, name, "failed", "timed out"))
            writer({"type": "task_timed_out", "task_id": task_id, "error": result.error})
            return f"Teammate '{name}' timed out: {result.error}"

        time.sleep(5)
        poll_count += 1
        if poll_count > max_poll_count:
            _swarm_db_op(_finalize_teammate(team.id, name, "failed", "polling timed out"))
            writer({"type": "task_timed_out", "task_id": task_id})
            return f"Teammate '{name}' polling timed out"
