"""Memory module for Nous Agent.

Enhanced with DeerFlow features:
- Correction/reinforcement detection (中英双语)
- Upload file path filtering (strips ephemeral paths)
- Fact deduplication (case-folded content matching)
- Upload mention stripping from LLM output
- ThreadPoolExecutor-based sync updates (no cross-loop bugs)
- PostgreSQL persistence (user_memory_sections + user_memory_facts)
"""

from src.agents.memory.message_processing import (
    detect_correction,
    detect_reinforcement,
    extract_message_text,
    filter_messages_for_memory,
)
from src.agents.memory.prompt import (
    FACT_EXTRACTION_PROMPT,
    MEMORY_UPDATE_PROMPT,
    format_conversation_for_update,
    format_memory_for_injection,
)
from src.agents.memory.queue import (
    ConversationContext,
    MemoryUpdateQueue,
    get_memory_queue,
    reset_memory_queue,
)
from src.agents.memory.updater import (
    MemoryUpdater,
    get_memory_data,
    update_memory_from_conversation,
)

__all__ = [
    # Message processing
    "detect_correction",
    "detect_reinforcement",
    "extract_message_text",
    "filter_messages_for_memory",
    # Prompt utilities
    "MEMORY_UPDATE_PROMPT",
    "FACT_EXTRACTION_PROMPT",
    "format_memory_for_injection",
    "format_conversation_for_update",
    # Queue
    "ConversationContext",
    "MemoryUpdateQueue",
    "get_memory_queue",
    "reset_memory_queue",
    # Updater
    "MemoryUpdater",
    "get_memory_data",
    "update_memory_from_conversation",
]
