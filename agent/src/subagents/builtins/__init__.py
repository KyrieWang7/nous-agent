"""Built-in subagent configurations."""

from .bash_agent import BASH_AGENT_CONFIG
from .explore_agent import EXPLORE_AGENT_CONFIG
from .general_purpose import GENERAL_PURPOSE_CONFIG
from .plan_agent import PLAN_AGENT_CONFIG
from .verification_agent import VERIFICATION_AGENT_CONFIG

__all__ = [
    "GENERAL_PURPOSE_CONFIG",
    "BASH_AGENT_CONFIG",
    "EXPLORE_AGENT_CONFIG",
    "PLAN_AGENT_CONFIG",
    "VERIFICATION_AGENT_CONFIG",
]

# Registry of built-in subagents
BUILTIN_SUBAGENTS = {
    "general-purpose": GENERAL_PURPOSE_CONFIG,
    "bash": BASH_AGENT_CONFIG,
    "explore": EXPLORE_AGENT_CONFIG,
    "plan": PLAN_AGENT_CONFIG,
    "verification": VERIFICATION_AGENT_CONFIG,
}
