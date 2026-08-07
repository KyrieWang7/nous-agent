"""Pluggable event store for run telemetry persistence.

Provides a base class and a PostgreSQL implementation for storing
run events (LLM calls, tool invocations, token usage, etc.).
"""

from __future__ import annotations

import json
import logging
from abc import ABC, abstractmethod
from datetime import datetime
from typing import Any

logger = logging.getLogger(__name__)


def _coerce_timestamp(value: Any) -> datetime | None:
    """Coerce a created_at value into a tz-aware datetime for asyncpg.

    asyncpg's binary protocol encodes parameters before the SQL `::timestamptz`
    cast applies, so an ISO string raises DataError. Accept both datetimes and
    ISO strings defensively; fall back to None (DB DEFAULT NOW()) on failure.
    """
    if value is None or isinstance(value, datetime):
        return value
    if isinstance(value, str):
        try:
            return datetime.fromisoformat(value)
        except ValueError:
            logger.warning("Invalid created_at timestamp %r; using DB default", value)
            return None
    return None


class RunEventStore(ABC):
    """Abstract base for run event persistence.

    Implementations receive batches of event dicts and persist them.
    The store is decoupled from the callback handler to allow swapping
    backends (Postgres, SQLite, in-memory, external services).
    """

    @abstractmethod
    async def put_batch(self, events: list[dict[str, Any]]) -> None:
        """Persist a batch of run events.

        Args:
            events: List of event dicts with keys:
                thread_id, run_id, event_type, category, content,
                metadata, created_at.
        """
        pass

    @abstractmethod
    async def get_events(
        self,
        run_id: str,
        *,
        event_type: str | None = None,
        category: str | None = None,
        limit: int = 100,
    ) -> list[dict[str, Any]]:
        """Retrieve events for a run with optional filtering.

        Args:
            run_id: The run identifier.
            event_type: Optional filter by event type.
            category: Optional filter by category.
            limit: Maximum number of events to return.

        Returns:
            List of event dicts ordered by created_at.
        """
        pass

    async def setup(self) -> None:
        """Create required tables/indexes. Called once at startup."""
        pass


class InMemoryRunEventStore(RunEventStore):
    """In-memory event store for testing and development."""

    def __init__(self) -> None:
        self._events: list[dict[str, Any]] = []

    async def put_batch(self, events: list[dict[str, Any]]) -> None:
        self._events.extend(events)

    async def get_events(
        self,
        run_id: str,
        *,
        event_type: str | None = None,
        category: str | None = None,
        limit: int = 100,
    ) -> list[dict[str, Any]]:
        results = [e for e in self._events if e.get("run_id") == run_id]
        if event_type:
            results = [e for e in results if e.get("event_type") == event_type]
        if category:
            results = [e for e in results if e.get("category") == category]
        return results[:limit]


RUN_EVENTS_TABLE_SQL = """
CREATE TABLE IF NOT EXISTS run_events (
    id BIGSERIAL PRIMARY KEY,
    thread_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'trace',
    content JSONB DEFAULT '{}',
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_run_events_run_id
    ON run_events(run_id, created_at);
CREATE INDEX IF NOT EXISTS idx_run_events_thread_id
    ON run_events(thread_id, created_at);
CREATE INDEX IF NOT EXISTS idx_run_events_type
    ON run_events(run_id, event_type);
"""

RUN_COMPLETIONS_TABLE_SQL = """
CREATE TABLE IF NOT EXISTS run_completions (
    id BIGSERIAL PRIMARY KEY,
    thread_id TEXT NOT NULL,
    run_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'completed',
    total_input_tokens INTEGER DEFAULT 0,
    total_output_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    llm_call_count INTEGER DEFAULT 0,
    lead_agent_tokens INTEGER DEFAULT 0,
    subagent_tokens INTEGER DEFAULT 0,
    middleware_tokens INTEGER DEFAULT 0,
    message_count INTEGER DEFAULT 0,
    first_human_message TEXT,
    last_ai_message TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ DEFAULT NOW(),
    duration_ms INTEGER
);

CREATE INDEX IF NOT EXISTS idx_run_completions_thread
    ON run_completions(thread_id, completed_at DESC);
"""


class PostgresRunEventStore(RunEventStore):
    """PostgreSQL-backed event store for production deployments.

    Uses asyncpg for high-performance batch inserts. Events are stored
    in a dedicated `run_events` table; completion summaries in `run_completions`.
    """

    _setup_done: bool = False

    async def setup(self) -> None:
        """Create run_events and run_completions tables if they don't exist."""
        if PostgresRunEventStore._setup_done:
            return
        from src.storage.database import get_db_connection

        async with get_db_connection() as conn:
            await conn.execute(RUN_EVENTS_TABLE_SQL)
            await conn.execute(RUN_COMPLETIONS_TABLE_SQL)

        PostgresRunEventStore._setup_done = True
        logger.info("Run telemetry tables verified/created")

    async def put_batch(self, events: list[dict[str, Any]]) -> None:
        """Insert a batch of events into run_events table."""
        if not events:
            return

        from src.storage.database import get_db_connection

        async with get_db_connection() as conn:
            await conn.executemany(
                """
                INSERT INTO run_events (thread_id, run_id, event_type, category, content, metadata, created_at)
                VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::timestamptz)
                """,
                [
                    (
                        e["thread_id"],
                        e["run_id"],
                        e["event_type"],
                        e.get("category", "trace"),
                        json.dumps(e.get("content", {}), default=str, ensure_ascii=False),
                        json.dumps(e.get("metadata", {}), default=str, ensure_ascii=False),
                        _coerce_timestamp(e.get("created_at")),
                    )
                    for e in events
                ],
            )

    async def get_events(
        self,
        run_id: str,
        *,
        event_type: str | None = None,
        category: str | None = None,
        limit: int = 100,
    ) -> list[dict[str, Any]]:
        """Query events for a run."""
        from src.storage.database import get_db_connection

        conditions = ["run_id = $1"]
        params: list[Any] = [run_id]

        if event_type:
            params.append(event_type)
            conditions.append(f"event_type = ${len(params)}")
        if category:
            params.append(category)
            conditions.append(f"category = ${len(params)}")

        params.append(limit)
        where = " AND ".join(conditions)

        async with get_db_connection() as conn:
            rows = await conn.fetch(
                f"""
                SELECT thread_id, run_id, event_type, category, content, metadata, created_at
                FROM run_events
                WHERE {where}
                ORDER BY created_at
                LIMIT ${len(params)}
                """,
                *params,
            )
            return [dict(r) for r in rows]

    async def save_completion(self, run_id: str, thread_id: str, data: dict[str, Any]) -> None:
        """Persist the run completion summary (token totals, bucketed costs).

        Args:
            run_id: The run identifier.
            thread_id: The thread identifier.
            data: Completion data dict from RunJournal.get_completion_data().
        """
        from src.storage.database import get_db_connection

        async with get_db_connection() as conn:
            await conn.execute(
                """
                INSERT INTO run_completions (
                    thread_id, run_id, status,
                    total_input_tokens, total_output_tokens, total_tokens,
                    llm_call_count,
                    lead_agent_tokens, subagent_tokens, middleware_tokens,
                    message_count, first_human_message, last_ai_message,
                    completed_at
                )
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
                ON CONFLICT (run_id) DO UPDATE SET
                    status = EXCLUDED.status,
                    total_input_tokens = EXCLUDED.total_input_tokens,
                    total_output_tokens = EXCLUDED.total_output_tokens,
                    total_tokens = EXCLUDED.total_tokens,
                    llm_call_count = EXCLUDED.llm_call_count,
                    lead_agent_tokens = EXCLUDED.lead_agent_tokens,
                    subagent_tokens = EXCLUDED.subagent_tokens,
                    middleware_tokens = EXCLUDED.middleware_tokens,
                    message_count = EXCLUDED.message_count,
                    first_human_message = EXCLUDED.first_human_message,
                    last_ai_message = EXCLUDED.last_ai_message,
                    completed_at = NOW()
                """,
                thread_id,
                run_id,
                data.get("status", "completed"),
                data.get("total_input_tokens", 0),
                data.get("total_output_tokens", 0),
                data.get("total_tokens", 0),
                data.get("llm_call_count", 0),
                data.get("lead_agent_tokens", 0),
                data.get("subagent_tokens", 0),
                data.get("middleware_tokens", 0),
                data.get("message_count", 0),
                data.get("first_human_message"),
                data.get("last_ai_message"),
            )
