"""Memory API router for retrieving and managing global memory data."""

import logging

from fastapi import APIRouter
from pydantic import BaseModel, Field
from src.config.memory_config import get_memory_config

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api", tags=["memory"])


class ContextSection(BaseModel):
    """Model for context sections (user and history)."""

    summary: str = Field(default="", description="Summary content")
    updatedAt: str = Field(default="", description="Last update timestamp")


class UserContext(BaseModel):
    """Model for user context."""

    workContext: ContextSection = Field(default_factory=ContextSection)
    personalContext: ContextSection = Field(default_factory=ContextSection)
    topOfMind: ContextSection = Field(default_factory=ContextSection)


class HistoryContext(BaseModel):
    """Model for history context."""

    recentMonths: ContextSection = Field(default_factory=ContextSection)
    earlierContext: ContextSection = Field(default_factory=ContextSection)
    longTermBackground: ContextSection = Field(default_factory=ContextSection)


class Fact(BaseModel):
    """Model for a memory fact."""

    id: str = Field(..., description="Unique identifier for the fact")
    content: str = Field(..., description="Fact content")
    category: str = Field(default="context", description="Fact category")
    confidence: float = Field(default=0.5, description="Confidence score (0-1)")
    createdAt: str = Field(default="", description="Creation timestamp")
    source: str = Field(default="unknown", description="Source thread ID")


class MemoryResponse(BaseModel):
    """Response model for memory data."""

    version: str = Field(default="1.0", description="Memory schema version")
    lastUpdated: str = Field(default="", description="Last update timestamp")
    user: UserContext = Field(default_factory=UserContext)
    history: HistoryContext = Field(default_factory=HistoryContext)
    facts: list[Fact] = Field(default_factory=list)


class MemoryConfigResponse(BaseModel):
    """Response model for memory configuration."""

    enabled: bool = Field(..., description="Whether memory is enabled")
    storage_path: str = Field(..., description="Path to memory storage file")
    debounce_seconds: int = Field(..., description="Debounce time for memory updates")
    max_facts: int = Field(..., description="Maximum number of facts to store")
    fact_confidence_threshold: float = Field(..., description="Minimum confidence threshold for facts")
    injection_enabled: bool = Field(..., description="Whether memory injection is enabled")
    max_injection_tokens: int = Field(..., description="Maximum tokens for memory injection")


class MemoryStatusResponse(BaseModel):
    """Response model for memory status."""

    config: MemoryConfigResponse
    data: MemoryResponse


@router.get(
    "/memory",
    response_model=MemoryResponse,
    summary="Get Memory Data",
    description="Retrieve the current global memory data including user context, history, and facts.",
)
async def get_memory() -> MemoryResponse:
    """Get the current global memory data from PostgreSQL.

    Returns:
        The current memory data with user context, history, and facts.
    """
    from src.agents.memory.updater import get_memory_data as _get_memory_data

    # TODO: resolve user_id from auth context. For now use a hardcoded default
    # or try to read from the first thread's owner.
    try:
        import asyncpg
        from src.storage.database import LANGGRAPH_PG_URI

        conn = await asyncpg.connect(LANGGRAPH_PG_URI)
        try:
            # Get the first available user_id from threads table
            user_id = await conn.fetchval(
                "SELECT user_id FROM threads ORDER BY created_at DESC LIMIT 1"
            )
            if not user_id:
                return MemoryResponse()

            user_id_str = str(user_id)
            memory_data = await _get_memory_data(user_id_str)

            # Convert to response model
            user_ctx = memory_data.get("user", {})
            history_ctx = memory_data.get("history", {})
            facts_raw = memory_data.get("facts", [])

            return MemoryResponse(
                version=memory_data.get("version", "1.0"),
                lastUpdated=memory_data.get("lastUpdated", ""),
                user=UserContext(
                    workContext=ContextSection(**user_ctx.get("workContext", {})),
                    personalContext=ContextSection(**user_ctx.get("personalContext", {})),
                    topOfMind=ContextSection(**user_ctx.get("topOfMind", {})),
                ),
                history=HistoryContext(
                    recentMonths=ContextSection(**history_ctx.get("recentMonths", {})),
                    earlierContext=ContextSection(**history_ctx.get("earlierContext", {})),
                    longTermBackground=ContextSection(**history_ctx.get("longTermBackground", {})),
                ),
                facts=[
                    Fact(
                        id=f.get("id", ""),
                        content=f.get("content", ""),
                        category=f.get("category", "context"),
                        confidence=f.get("confidence", 0.5),
                        createdAt=f.get("createdAt", ""),
                        source=f.get("source", "unknown"),
                    )
                    for f in facts_raw
                    if isinstance(f, dict) and f.get("id")
                ],
            )
        finally:
            await conn.close()
    except Exception as e:
        logger.warning("Failed to load memory from PostgreSQL: %s", e)
        return MemoryResponse()


@router.post(
    "/memory/reload",
    response_model=MemoryResponse,
    summary="Reload Memory Data",
    description="Reload memory data from PostgreSQL (no-op in multi-user mode, kept for API compat).",
)
async def reload_memory() -> MemoryResponse:
    """Reload memory data.

    In the PostgreSQL-backed multi-user architecture, memory is always
    read from the database.  This endpoint is kept for API compatibility.
    """
    return MemoryResponse()


@router.get(
    "/memory/config",
    response_model=MemoryConfigResponse,
    summary="Get Memory Configuration",
    description="Retrieve the current memory system configuration.",
)
async def get_memory_config_endpoint() -> MemoryConfigResponse:
    """Get the memory system configuration.

    Returns:
        The current memory configuration settings.

    Example Response:
        ```json
        {
            "enabled": true,
            "storage_path": ".deer-flow/memory.json",
            "debounce_seconds": 30,
            "max_facts": 100,
            "fact_confidence_threshold": 0.7,
            "injection_enabled": true,
            "max_injection_tokens": 2000
        }
        ```
    """
    config = get_memory_config()
    return MemoryConfigResponse(
        enabled=config.enabled,
        storage_path=config.storage_path,
        debounce_seconds=config.debounce_seconds,
        max_facts=config.max_facts,
        fact_confidence_threshold=config.fact_confidence_threshold,
        injection_enabled=config.injection_enabled,
        max_injection_tokens=config.max_injection_tokens,
    )


@router.get(
    "/memory/status",
    response_model=MemoryStatusResponse,
    summary="Get Memory Status",
    description="Retrieve both memory configuration and current data in a single request.",
)
async def get_memory_status() -> MemoryStatusResponse:
    """Get the memory system status including configuration and data.

    Returns:
        Combined memory configuration and current data.
    """
    config = get_memory_config()
    return MemoryStatusResponse(
        config=MemoryConfigResponse(
            enabled=config.enabled,
            storage_path=config.storage_path,
            debounce_seconds=config.debounce_seconds,
            max_facts=config.max_facts,
            fact_confidence_threshold=config.fact_confidence_threshold,
            injection_enabled=config.injection_enabled,
            max_injection_tokens=config.max_injection_tokens,
        ),
        data=MemoryResponse(),
    )
