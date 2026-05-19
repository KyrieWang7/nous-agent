"""Context management: compaction, token budgets, and continuation contracts.

Provides deterministic local summarisation of conversation history to manage
token budgets without requiring an LLM call for every compression pass.
"""

from src.context.budget import TokenBudget
from src.context.compaction import CompactionConfig, CompactionEngine, CompactionResult

__all__ = ["CompactionConfig", "CompactionEngine", "CompactionResult", "TokenBudget"]
