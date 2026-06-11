import logging

from langchain.tools import BaseTool

from src.config import get_app_config
from src.reflection import resolve_variable
from src.tools.builtins import ask_clarification_tool, present_file_tool, task_tool, view_image_tool

logger = logging.getLogger(__name__)

BUILTIN_TOOLS = [
    present_file_tool,
    ask_clarification_tool,
]

SUBAGENT_TOOLS = [
    task_tool,
    # task_status_tool is no longer exposed to LLM (backend handles polling internally)
]


def get_available_tools(
    groups: list[str] | None = None,
    include_mcp: bool = True,
    model_name: str | None = None,
    subagent_enabled: bool = False,
    swarm_enabled: bool = False,
) -> list[BaseTool]:
    """Get all available tools from config.

    Note: MCP tools should be initialized at application startup using
    `initialize_mcp_tools()` from src.mcp module.

    Args:
        groups: Optional list of tool groups to filter by.
        include_mcp: Whether to include tools from MCP servers (default: True).
        model_name: Optional model name to determine if vision tools should be included.
        subagent_enabled: Whether to include subagent tools (task, task_status).
        swarm_enabled: Whether to include swarm team tools (team_create, team_delete, list_teammates).

    Returns:
        List of available tools.
    """
    config = get_app_config()
    loaded_tools = [resolve_variable(tool.use, BaseTool) for tool in config.tools if groups is None or tool.group in groups]

    # If sandbox is disabled, filter out sandbox-dependent tools (file:read, file:write, bash)
    if not config.sandbox.enabled:
        _SANDBOX_GROUPS = {"file:read", "file:write", "bash"}
        loaded_tools = [
            resolve_variable(tool.use, BaseTool)
            for tool in config.tools
            if (groups is None or tool.group in groups) and tool.group not in _SANDBOX_GROUPS
        ]
        logger.info("Sandbox disabled — excluded tools from groups: %s", _SANDBOX_GROUPS)

    # Get cached MCP tools if enabled
    # NOTE: We use ExtensionsConfig.from_file() instead of config.extensions
    # to always read the latest configuration from disk. This ensures that changes
    # made through the Gateway API (which runs in a separate process) are immediately
    # reflected when loading MCP tools.
    mcp_tools = []
    if include_mcp:
        try:
            from src.config.extensions_config import ExtensionsConfig
            from src.mcp.cache import get_cached_mcp_tools

            extensions_config = ExtensionsConfig.from_file()
            if extensions_config.get_enabled_mcp_servers():
                mcp_tools = get_cached_mcp_tools()
                if mcp_tools:
                    logger.info(f"Using {len(mcp_tools)} cached MCP tool(s)")
        except ImportError:
            logger.warning("MCP module not available. Install 'langchain-mcp-adapters' package to enable MCP tools.")
        except Exception as e:
            logger.error(f"Failed to get cached MCP tools: {e}")

    # Conditionally add tools based on config
    builtin_tools = BUILTIN_TOOLS.copy()

    # Add subagent tools only if enabled via runtime parameter
    if subagent_enabled:
        builtin_tools.extend(SUBAGENT_TOOLS)
        logger.info("Including subagent tools (task)")

    # If no model_name specified, use the first model (default)
    if model_name is None and config.models:
        model_name = config.models[0].name

    # Add view_image_tool only if the model supports vision
    model_config = config.get_model_config(model_name) if model_name else None
    if model_config is not None and model_config.supports_vision:
        builtin_tools.append(view_image_tool)
        logger.info(f"Including view_image_tool for model '{model_name}' (supports_vision=True)")

    # Add plugin tools if plugins system is enabled
    plugin_tools = []
    try:
        from src.config.plugins_config import get_plugins_config
        from src.plugins.loader import discover_plugins
        from src.plugins.tools import create_plugin_tools

        plugins_cfg = get_plugins_config()
        if plugins_cfg.enabled and plugins_cfg.directories:
            registry = discover_plugins(plugins_cfg.directories)
            plugin_tools = create_plugin_tools(registry)
            if plugin_tools:
                logger.info(f"Loaded {len(plugin_tools)} plugin tool(s)")
    except ImportError:
        logger.debug("Plugin system not available; skipping plugin tools")
    except Exception as e:
        logger.error(f"Failed to load plugin tools: {e}")

    # Add ACP invoke tool if any ACP agents are configured
    acp_tools: list[BaseTool] = []
    try:
        from src.config.acp_config import get_acp_agents
        from src.tools.builtins import build_invoke_acp_agent_tool

        if get_acp_agents():
            acp_tool = build_invoke_acp_agent_tool()
            acp_tools.append(acp_tool)
            logger.info("Including invoke_acp_agent tool for configured ACP agents")
    except ImportError:
        logger.debug("ACP module not available; skipping invoke_acp_agent tool")
    except Exception as e:
        logger.error(f"Failed to build ACP tool: {e}")

    # Add swarm tools when enabled via runtime parameter (send_message is bound per teammate in TeammateSpawner)
    swarm_tools: list[BaseTool] = []
    try:
        if swarm_enabled:
            from src.tools.builtins.list_teammates_tool import list_teammates_tool
            from src.tools.builtins.team_create_tool import team_create_tool
            from src.tools.builtins.team_delete_tool import team_delete_tool

            swarm_tools = [team_create_tool, team_delete_tool, list_teammates_tool]
            logger.info("Including swarm tools (team_create, team_delete, list_teammates)")
    except ImportError:
        logger.debug("Swarm module not available; skipping swarm tools")
    except Exception as e:
        logger.error(f"Failed to load swarm tools: {e}")

    all_tools = loaded_tools + builtin_tools + mcp_tools + plugin_tools + acp_tools + swarm_tools

    if swarm_enabled and subagent_enabled:
        # In swarm mode the lead agent is a pure coordinator — strip execution
        # tools so the model is forced to delegate via `task`.
        coordinator_names = {
            "task", "team_create", "team_delete", "list_teammates",
            "present_file", "ask_clarification", "view_image",
        }
        all_tools = [t for t in all_tools if t.name in coordinator_names]
        logger.info(
            "Swarm coordinator mode: keeping only %s tools",
            [t.name for t in all_tools],
        )

    # Skill-declared tool policy: when any enabled skill declares ``allowed-tools``
    # in its frontmatter, restrict the bound tools to the union of those
    # declarations. Skills without the field contribute nothing (legacy allow-all
    # only applies when NO skill declares the field), so this is a no-op until a
    # skill opts in. Ported from deer-flow.
    if getattr(config.skills, "tool_policy_enabled", True):
        try:
            from src.skills.loader import load_skills
            from src.skills.tool_policy import filter_tools_by_skill_allowed_tools

            enabled_skills = load_skills(enabled_only=True)
            before = len(all_tools)
            all_tools = filter_tools_by_skill_allowed_tools(all_tools, enabled_skills)
            if len(all_tools) != before:
                logger.info(
                    "Skill tool policy applied: %d -> %d tools (allowlist=%s)",
                    before,
                    len(all_tools),
                    sorted(t.name for t in all_tools),
                )
        except Exception:
            logger.exception("Skill tool policy filtering failed; leaving tools unfiltered")

    return all_tools
