import logging

from deepagents.middleware.summarization import SummarizationMiddleware
from langchain.agents import create_agent
from langchain.agents.middleware import TodoListMiddleware

try:
    from deepagents.middleware.summarization import SummarizationToolMiddleware, compute_summarization_defaults
except ImportError:
    SummarizationToolMiddleware = None  # type: ignore[assignment,misc]
    compute_summarization_defaults = None  # type: ignore[assignment]
from langchain_core.runnables import RunnableConfig

from src.agents.lead_agent.prompt import apply_prompt_template
from src.agents.middlewares.clarification_middleware import ClarificationMiddleware
from src.agents.middlewares.dangling_tool_call_middleware import DanglingToolCallMiddleware
from src.agents.middlewares.memory_middleware import MemoryMiddleware
from src.agents.middlewares.postgres_backend import PostgresBackend
from src.agents.middlewares.subagent_limit_middleware import SubagentLimitMiddleware
from src.agents.middlewares.thread_data_middleware import ThreadDataMiddleware
from src.agents.middlewares.title_middleware import TitleMiddleware
from src.agents.middlewares.uploads_middleware import UploadsMiddleware
from src.agents.middlewares.view_image_middleware import ViewImageMiddleware
from src.agents.thread_state import ThreadState
from src.config.app_config import get_app_config
from src.config.summarization_config import get_summarization_config
from src.models import create_chat_model
from src.sandbox.middleware import SandboxMiddleware

logger = logging.getLogger(__name__)


def _resolve_model_name(requested_model_name: str | None) -> str:
    """Resolve a runtime model name safely, falling back to default if invalid. Returns None if no models are configured."""
    app_config = get_app_config()
    default_model_name = app_config.models[0].name if app_config.models else None
    if default_model_name is None:
        raise ValueError(
            "No chat models are configured. Please configure at least one model in config.yaml."
        )

    if requested_model_name and app_config.get_model_config(requested_model_name):
        return requested_model_name

    if requested_model_name and requested_model_name != default_model_name:
        logger.warning(f"Model '{requested_model_name}' not found in config; fallback to default model '{default_model_name}'.")
    return default_model_name


def _create_summarization_middlewares() -> tuple | None:
    """Create and configure the summarization middleware (+ optional tool middleware) from config.

    Returns a tuple of middlewares or None if disabled.
    """
    config = get_summarization_config()

    if not config.enabled:
        return None

    # Prepare trigger parameter
    trigger = None
    if config.trigger is not None:
        if isinstance(config.trigger, list):
            trigger = [t.to_tuple() for t in config.trigger]
        else:
            trigger = config.trigger.to_tuple()

    # Prepare keep parameter
    keep = config.keep.to_tuple()

    # Prepare model parameter
    if config.model_name:
        model = config.model_name
    else:
        model = create_chat_model(thinking_enabled=False)

    # When trigger is not explicitly configured, try model-aware defaults
    if trigger is None and not isinstance(model, str) and compute_summarization_defaults is not None:
        try:
            defaults = compute_summarization_defaults(model)
            trigger = defaults["trigger"]
            keep = defaults["keep"]
            if config.truncate_args is None:
                pass  # will be set from defaults below
        except Exception:
            logger.debug("compute_summarization_defaults failed, using config values")

    # Prepare kwargs
    kwargs = {
        "model": model,
        "backend": PostgresBackend(),
        "trigger": trigger,
        "keep": keep,
    }

    if config.trim_tokens_to_summarize is not None:
        kwargs["trim_tokens_to_summarize"] = config.trim_tokens_to_summarize

    if config.summary_prompt is not None:
        kwargs["summary_prompt"] = config.summary_prompt

    # Build truncate_args_settings from config or model-aware defaults
    if config.truncate_args is not None:
        truncate_settings = {}
        if config.truncate_args.trigger is not None:
            truncate_settings["trigger"] = config.truncate_args.trigger.to_tuple()
        if config.truncate_args.keep is not None:
            truncate_settings["keep"] = config.truncate_args.keep.to_tuple()
        if config.truncate_args.max_length is not None:
            truncate_settings["max_length"] = config.truncate_args.max_length
        if config.truncate_args.truncation_text is not None:
            truncate_settings["truncation_text"] = config.truncate_args.truncation_text
        kwargs["truncate_args_settings"] = truncate_settings
    elif not isinstance(model, str) and "truncate_args_settings" not in kwargs and compute_summarization_defaults is not None:
        try:
            defaults = compute_summarization_defaults(model)
            if "truncate_args_settings" in defaults:
                kwargs["truncate_args_settings"] = defaults["truncate_args_settings"]
        except Exception:
            pass

    summ_mw = SummarizationMiddleware(**kwargs)
    middlewares = [summ_mw]

    if SummarizationToolMiddleware is not None:
        tool_mw = SummarizationToolMiddleware(summ_mw)
        middlewares.append(tool_mw)

    return tuple(middlewares)


def _create_todo_list_middleware(is_plan_mode: bool) -> TodoListMiddleware | None:
    """Create and configure the TodoList middleware.

    Args:
        is_plan_mode: Whether to enable plan mode with TodoList middleware.

    Returns:
        TodoListMiddleware instance if plan mode is enabled, None otherwise.
    """
    if not is_plan_mode:
        return None

    # Custom prompts matching DeerFlow's style
    system_prompt = """
<todo_list_system>
You have access to the `write_todos` tool to help you manage and track complex multi-step objectives.

**CRITICAL RULES:**
- Mark todos as completed IMMEDIATELY after finishing each step - do NOT batch completions
- Keep EXACTLY ONE task as `in_progress` at any time (unless tasks can run in parallel)
- Update the todo list in REAL-TIME as you work - this gives users visibility into your progress
- DO NOT use this tool for simple tasks (< 3 steps) - just complete them directly

**When to Use:**
This tool is designed for complex objectives that require systematic tracking:
- Complex multi-step tasks requiring 3+ distinct steps
- Non-trivial tasks needing careful planning and execution
- User explicitly requests a todo list
- User provides multiple tasks (numbered or comma-separated list)
- The plan may need revisions based on intermediate results

**When NOT to Use:**
- Single, straightforward tasks
- Trivial tasks (< 3 steps)
- Purely conversational or informational requests
- Simple tool calls where the approach is obvious

**Best Practices:**
- Break down complex tasks into smaller, actionable steps
- Use clear, descriptive task names
- Remove tasks that become irrelevant
- Add new tasks discovered during implementation
- Don't be afraid to revise the todo list as you learn more

**Task Management:**
Writing todos takes time and tokens - use it when helpful for managing complex problems, not for simple requests.
</todo_list_system>
"""

    tool_description = """Use this tool to create and manage a structured task list for complex work sessions.

**IMPORTANT: Only use this tool for complex tasks (3+ steps). For simple requests, just do the work directly.**

## When to Use

Use this tool in these scenarios:
1. **Complex multi-step tasks**: When a task requires 3 or more distinct steps or actions
2. **Non-trivial tasks**: Tasks requiring careful planning or multiple operations
3. **User explicitly requests todo list**: When the user directly asks you to track tasks
4. **Multiple tasks**: When users provide a list of things to be done
5. **Dynamic planning**: When the plan may need updates based on intermediate results

## When NOT to Use

Skip this tool when:
1. The task is straightforward and takes less than 3 steps
2. The task is trivial and tracking provides no benefit
3. The task is purely conversational or informational
4. It's clear what needs to be done and you can just do it

## How to Use

1. **Starting a task**: Mark it as `in_progress` BEFORE beginning work
2. **Completing a task**: Mark it as `completed` IMMEDIATELY after finishing
3. **Updating the list**: Add new tasks, remove irrelevant ones, or update descriptions as needed
4. **Multiple updates**: You can make several updates at once (e.g., complete one task and start the next)

## Task States

- `pending`: Task not yet started
- `in_progress`: Currently working on (can have multiple if tasks run in parallel)
- `completed`: Task finished successfully

## Task Completion Requirements

**CRITICAL: Only mark a task as completed when you have FULLY accomplished it.**

Never mark a task as completed if:
- There are unresolved issues or errors
- Work is partial or incomplete
- You encountered blockers preventing completion
- You couldn't find necessary resources or dependencies
- Quality standards haven't been met

If blocked, keep the task as `in_progress` and create a new task describing what needs to be resolved.

## Best Practices

- Create specific, actionable items
- Break complex tasks into smaller, manageable steps
- Use clear, descriptive task names
- Update task status in real-time as you work
- Mark tasks complete IMMEDIATELY after finishing (don't batch completions)
- Remove tasks that are no longer relevant
- **IMPORTANT**: When you write the todo list, mark your first task(s) as `in_progress` immediately
- **IMPORTANT**: Unless all tasks are completed, always have at least one task `in_progress` to show progress

Being proactive with task management demonstrates thoroughness and ensures all requirements are completed successfully.

**Remember**: If you only need a few tool calls to complete a task and it's clear what to do, it's better to just do the task directly and NOT use this tool at all.
"""

    return TodoListMiddleware(system_prompt=system_prompt, tool_description=tool_description)


def _maybe_add_permission_middleware(chain: list) -> None:
    """Add PermissionMiddleware if permissions are configured and not in allow-all mode."""
    from src.config.permissions_config import get_permissions_config
    from src.permissions.middleware import PermissionMiddleware
    from src.permissions.mode import PermissionMode
    from src.permissions.policy import PermissionPolicy

    cfg = get_permissions_config()
    if not cfg.enabled:
        return

    mode_map = {
        "allow": PermissionMode.ALLOW,
        "prompt": PermissionMode.PROMPT,
        "danger_full_access": PermissionMode.DANGER_FULL_ACCESS,
        "workspace_write": PermissionMode.WORKSPACE_WRITE,
        "read_only": PermissionMode.READ_ONLY,
    }
    active = mode_map.get(cfg.mode, PermissionMode.ALLOW)

    if active == PermissionMode.ALLOW and not cfg.tool_overrides:
        return

    policy = PermissionPolicy(active_mode=active)
    for tool_name, mode_str in cfg.tool_overrides.items():
        mode = mode_map.get(mode_str, PermissionMode.DANGER_FULL_ACCESS)
        policy = policy.with_tool_requirement(tool_name, mode)

    chain.append(PermissionMiddleware(policy))


def _maybe_add_guardrail_middleware(chain: list) -> None:
    """Add GuardrailMiddleware if guardrails are configured and enabled."""
    import inspect

    from src.config.guardrails_config import get_guardrails_config
    from src.guardrails.middleware import GuardrailMiddleware
    from src.reflection import resolve_variable

    cfg = get_guardrails_config()
    if not cfg.enabled or not cfg.provider:
        return

    provider_cls = resolve_variable(cfg.provider.use)
    provider_kwargs = dict(cfg.provider.config) if cfg.provider.config else {}
    if "framework" not in provider_kwargs:
        try:
            sig = inspect.signature(provider_cls.__init__)
            if "framework" in sig.parameters or any(
                p.kind == inspect.Parameter.VAR_KEYWORD for p in sig.parameters.values()
            ):
                provider_kwargs["framework"] = "nous-agent"
        except (ValueError, TypeError):
            pass
    provider = provider_cls(**provider_kwargs)
    chain.append(GuardrailMiddleware(provider, fail_closed=cfg.fail_closed, passport=cfg.passport))


def _maybe_add_hook_middleware(chain: list) -> None:
    """Add HookMiddleware if hooks are configured and enabled."""
    from src.config.hooks_config import get_hooks_config
    from src.hooks.middleware import HookMiddleware
    from src.hooks.runner import HookRunner

    cfg = get_hooks_config()
    if not cfg.enabled:
        return

    raw: dict = {}
    if cfg.pre_tool_use:
        raw["pre_tool_use"] = [h.model_dump(exclude_none=True) for h in cfg.pre_tool_use]
    if cfg.post_tool_use:
        raw["post_tool_use"] = [h.model_dump(exclude_none=True) for h in cfg.post_tool_use]
    if cfg.post_tool_use_failure:
        raw["post_tool_use_failure"] = [h.model_dump(exclude_none=True) for h in cfg.post_tool_use_failure]

    if raw:
        runner = HookRunner.from_config(raw)
        chain.append(HookMiddleware(runner))


def _maybe_add_inbox_poller_middleware(chain: list, config: RunnableConfig) -> None:
    """Add InboxPollerMiddleware if swarm is on via runtime configurable or YAML."""
    try:
        from src.config.swarm_config import get_swarm_config

        runtime_swarm = config.get("configurable", {}).get("swarm_enabled", False)
        if not runtime_swarm and not get_swarm_config().enabled:
            return

        from src.agents.middlewares.inbox_poller_middleware import InboxPollerMiddleware

        chain.append(InboxPollerMiddleware())
    except ImportError:
        logger.debug("Swarm module not available; skipping InboxPollerMiddleware")


def _maybe_add_compaction_middleware(chain: list) -> None:
    """Add CompactionMiddleware if context compaction is available."""
    try:
        from src.context.middleware import CompactionMiddleware

        chain.append(CompactionMiddleware())
    except ImportError:
        logger.debug("CompactionMiddleware not available; skipping")


# ThreadDataMiddleware must be before SandboxMiddleware to ensure thread_id is available
# UploadsMiddleware should be after ThreadDataMiddleware to access thread_id
# DanglingToolCallMiddleware patches missing ToolMessages before model sees the history
# SummarizationMiddleware should be early to reduce context before other processing
# TodoListMiddleware should be before ClarificationMiddleware to allow todo management
# TitleMiddleware generates title after first exchange
# MemoryMiddleware queues conversation for memory update (after TitleMiddleware)
# ViewImageMiddleware should be before ClarificationMiddleware to inject image details before LLM
# ClarificationMiddleware should be last to intercept clarification requests after model calls
def _build_middlewares(config: RunnableConfig, model_name: str | None):
    """Build middleware chain based on runtime configuration.

    Args:
        config: Runtime configuration containing configurable options like is_plan_mode.

    Returns:
        List of middleware instances.
    """
    middlewares = [ThreadDataMiddleware(), UploadsMiddleware()]

    # Add SandboxMiddleware only if sandbox is enabled
    app_config = get_app_config()
    if app_config.sandbox.enabled:
        middlewares.append(SandboxMiddleware())
    else:
        logger.info("Sandbox disabled — skipping SandboxMiddleware and file/bash tools")

    middlewares.append(DanglingToolCallMiddleware())

    # Add PermissionMiddleware if configured
    _maybe_add_permission_middleware(middlewares)

    # Add GuardrailMiddleware if configured
    _maybe_add_guardrail_middleware(middlewares)

    # Add HookMiddleware if configured
    _maybe_add_hook_middleware(middlewares)

    # Add SandboxAuditMiddleware (bash command security auditing)
    from src.agents.middlewares.sandbox_audit_middleware import SandboxAuditMiddleware
    middlewares.append(SandboxAuditMiddleware())

    # Add ToolErrorHandlingMiddleware (convert tool exceptions to error ToolMessages)
    from src.agents.middlewares.tool_error_handling_middleware import ToolErrorHandlingMiddleware
    middlewares.append(ToolErrorHandlingMiddleware())

    # Add summarization middleware (+ optional compact_conversation tool middleware)
    summ_pair = _create_summarization_middlewares()
    if summ_pair is not None:
        middlewares.extend(summ_pair)

    # Add CompactionMiddleware if available
    _maybe_add_compaction_middleware(middlewares)

    # Add TodoList middleware if plan mode is enabled
    is_plan_mode = config.get("configurable", {}).get("is_plan_mode", False)
    todo_list_middleware = _create_todo_list_middleware(is_plan_mode)
    if todo_list_middleware is not None:
        middlewares.append(todo_list_middleware)

    # Add TokenUsageMiddleware (logs LLM token consumption)
    from src.agents.middlewares.token_usage_middleware import TokenUsageMiddleware
    middlewares.append(TokenUsageMiddleware())

    # Add TitleMiddleware
    middlewares.append(TitleMiddleware())

    # Add MemoryMiddleware (after TitleMiddleware)
    middlewares.append(MemoryMiddleware())

    # Add ViewImageMiddleware only if the current model supports vision.
    app_config = get_app_config()
    model_config = app_config.get_model_config(model_name) if model_name else None
    if model_config is not None and model_config.supports_vision:
        middlewares.append(ViewImageMiddleware())

    # Add DeferredToolFilterMiddleware (filters deferred tool schemas from model binding)
    from src.agents.middlewares.deferred_tool_filter_middleware import DeferredToolFilterMiddleware
    middlewares.append(DeferredToolFilterMiddleware())

    # Add SubagentLimitMiddleware to truncate excess parallel task calls
    subagent_enabled = config.get("configurable", {}).get("subagent_enabled", False)
    if config.get("configurable", {}).get("swarm_enabled", False):
        subagent_enabled = True
    if subagent_enabled:
        max_concurrent_subagents = config.get("configurable", {}).get("max_concurrent_subagents", 3)
        middlewares.append(SubagentLimitMiddleware(max_concurrent=max_concurrent_subagents))

    # Add LoopDetectionMiddleware (detects and breaks repetitive tool call loops)
    from src.agents.middlewares.loop_detection_middleware import LoopDetectionMiddleware
    middlewares.append(LoopDetectionMiddleware())

    # Add InboxPollerMiddleware if swarm mode is enabled (runtime or YAML)
    _maybe_add_inbox_poller_middleware(middlewares, config)

    # ClarificationMiddleware should always be last
    middlewares.append(ClarificationMiddleware())
    return middlewares


def make_lead_agent(config: RunnableConfig, checkpointer=None):
    # Lazy import to avoid circular dependency
    from src.tools import get_available_tools

    thinking_enabled = config.get("configurable", {}).get("thinking_enabled", True)
    reasoning_effort = config.get("configurable", {}).get("reasoning_effort", None)
    requested_model_name = config.get("configurable", {}).get("model_name") or config.get("configurable", {}).get("model")
    model_name = _resolve_model_name(requested_model_name)
    if model_name is None:
        raise ValueError(
            "No chat model could be resolved. Please configure at least one model in "
            "config.yaml or provide a valid 'model_name'/'model' in the request."
        )
    is_plan_mode = config.get("configurable", {}).get("is_plan_mode", False)
    subagent_enabled = config.get("configurable", {}).get("subagent_enabled", False)
    swarm_enabled = config.get("configurable", {}).get("swarm_enabled", False)
    if swarm_enabled:
        subagent_enabled = True
    max_concurrent_subagents = config.get("configurable", {}).get("max_concurrent_subagents", 3)

    app_config = get_app_config()
    model_config = app_config.get_model_config(model_name) if model_name else None
    if thinking_enabled and model_config is not None and not model_config.supports_thinking:
        logger.warning(f"Thinking mode is enabled but model '{model_name}' does not support it; fallback to non-thinking mode.")
        thinking_enabled = False

    logger.info(
        "thinking_enabled: %s, reasoning_effort: %s, model_name: %s, is_plan_mode: %s, subagent_enabled: %s, max_concurrent_subagents: %s, swarm_enabled: %s",
        thinking_enabled,
        reasoning_effort,
        model_name,
        is_plan_mode,
        subagent_enabled,
        max_concurrent_subagents,
        swarm_enabled,
    )

    # Inject run metadata for LangSmith trace tagging
    if "metadata" not in config:
        config["metadata"] = {}
    config["metadata"].update(
        {
            "model_name": model_name or "default",
            "thinking_enabled": thinking_enabled,
            "reasoning_effort": reasoning_effort,
            "is_plan_mode": is_plan_mode,
            "subagent_enabled": subagent_enabled,
            "swarm_enabled": swarm_enabled,
        }
    )

    return create_agent(
        model=create_chat_model(name=model_name, thinking_enabled=thinking_enabled, reasoning_effort=reasoning_effort),
        tools=get_available_tools(model_name=model_name, subagent_enabled=subagent_enabled, swarm_enabled=swarm_enabled),
        middleware=_build_middlewares(config, model_name=model_name),
        system_prompt=apply_prompt_template(
            subagent_enabled=subagent_enabled,
            max_concurrent_subagents=max_concurrent_subagents,
            swarm_enabled=swarm_enabled,
            swarm_team_name=config.get("configurable", {}).get("swarm_team_name"),
            swarm_team_id=config.get("configurable", {}).get("swarm_team_id"),
        ),
        state_schema=ThreadState,
        checkpointer=checkpointer,
    )

