"""Database connection management for the LangGraph backend.

Provides:
- ``get_checkpointer()``: Lazy-init ``AsyncPostgresSaver`` for LangGraph
  checkpoint persistence (conversation state / short-term memory).
- ``get_db_connection()``: Raw ``asyncpg`` connection from a shared pool,
  used by ``SessionManager`` to read/write the ``threads`` table that
  lives in the same Postgres instance.
- ``close_db()``: Graceful shutdown of both checkpointer and pool.

Reference: agon-multi-agent ``backend/app/core/database.py``
"""

from __future__ import annotations

import logging
import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import asyncpg
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

logger = logging.getLogger(__name__)

LANGGRAPH_PG_URI = os.environ.get(
    "LANGGRAPH_PG_URI",
    "postgresql://agno:agno_password@localhost:5432/agno_db",
)

_pool: asyncpg.Pool | None = None
_checkpointer: AsyncPostgresSaver | None = None
_checkpointer_pool: AsyncConnectionPool | None = None


async def _init_pool() -> asyncpg.Pool:
    """Create (or return existing) asyncpg connection pool."""
    global _pool
    if _pool is None:
        logger.info("Initialising asyncpg pool: %s", LANGGRAPH_PG_URI.split("@")[-1])
        _pool = await asyncpg.create_pool(LANGGRAPH_PG_URI, min_size=2, max_size=10)
    return _pool


async def get_checkpointer() -> AsyncPostgresSaver:
    """Return a shared ``AsyncPostgresSaver`` instance.

    The saver is backed by an ``AsyncConnectionPool`` (rather than a single
    long-lived connection from ``from_conn_string``).  A single connection
    dies permanently on idle timeout, postgres restart, or a network blip —
    after which every ``aget_tuple`` raises ``the connection is closed``.

    The pool health-checks each connection on checkout (``check_connection``)
    and recycles dead ones, so transient DB drops self-heal instead of
    wedging all subsequent runs.  Subsequent calls return the cached instance.
    """
    global _checkpointer, _checkpointer_pool

    if _checkpointer is None:
        logger.info("Initialising AsyncPostgresSaver (pooled)")
        # AsyncPostgresSaver requires autocommit + dict_row connections.
        _checkpointer_pool = AsyncConnectionPool(
            conninfo=LANGGRAPH_PG_URI,
            min_size=2,
            max_size=10,
            open=False,
            # Validate connections on checkout; dead ones are dropped and
            # replaced transparently instead of surfacing as runtime errors.
            check=AsyncConnectionPool.check_connection,
            kwargs={"autocommit": True, "prepare_threshold": 0, "row_factory": dict_row},
        )
        await _checkpointer_pool.open(wait=True)
        _checkpointer = AsyncPostgresSaver(_checkpointer_pool)
        await _checkpointer.setup()

        from src.storage.session_manager import setup_threads_table
        await setup_threads_table()

        from src.storage.history import setup_history_tables
        await setup_history_tables()

        from src.runtime.event_store import PostgresRunEventStore
        _run_event_store = PostgresRunEventStore()
        await _run_event_store.setup()

        from src.config.swarm_config import get_swarm_config
        if get_swarm_config().enabled:
            from src.swarm.schema import setup_swarm_tables
            await setup_swarm_tables()

        logger.info("AsyncPostgresSaver ready (%s)", LANGGRAPH_PG_URI.split("@")[-1])

    return _checkpointer


@asynccontextmanager
async def get_db_connection() -> AsyncIterator[asyncpg.Connection]:
    """Acquire a raw asyncpg connection from the shared pool."""
    pool = await _init_pool()
    async with pool.acquire() as conn:
        yield conn


@asynccontextmanager
async def get_standalone_connection() -> AsyncIterator[asyncpg.Connection]:
    """Open a standalone asyncpg connection (not from the pool).

    Use this when running in a separate thread with its own event loop
    (e.g. ``asyncio.run()`` inside a daemon Timer thread), where the
    pool — bound to the main loop — is not available.
    """
    conn = await asyncpg.connect(LANGGRAPH_PG_URI)
    try:
        yield conn
    finally:
        await conn.close()


async def close_db() -> None:
    """Shut down checkpointer and connection pool gracefully."""
    global _checkpointer, _checkpointer_pool, _pool

    if _checkpointer_pool is not None:
        await _checkpointer_pool.close()
        _checkpointer_pool = None
        _checkpointer = None

    if _pool is not None:
        await _pool.close()
        _pool = None
        logger.info("Database connections closed")
