"""Custom LangGraph-compatible API server.

Replaces ``langgraph dev`` / ``langgraph_runtime_inmem`` with a self-managed
FastAPI application.  All state (threads, runs, checkpoints) is persisted to
PostgreSQL via ``AsyncPostgresSaver``.

The server implements the subset of the LangGraph Platform API that the
frontend ``@langchain/langgraph-sdk`` client requires.

EventBus Architecture:
- Agent tasks run in background asyncio.Tasks (producer)
- SSE endpoints subscribe to EventBus queues (consumer)
- SSE disconnect does NOT cancel the background task
- Reconnection replays buffered events or falls back to checkpoint
- Optional Redis Streams dual-write for persistent event recovery
"""

from __future__ import annotations

import logging
import os
from contextlib import asynccontextmanager

from dotenv import load_dotenv

load_dotenv()

from fastapi import FastAPI  # noqa: E402
from fastapi.middleware.cors import CORSMiddleware  # noqa: E402

from src.api import (  # noqa: E402
    assistants_router,
    info_router,
    runs_router,
    thread_state_router,
    threads_router,
)
from src.core.event_bus import get_event_bus  # noqa: E402
from src.storage.database import close_db, get_checkpointer  # noqa: E402

logger = logging.getLogger(__name__)


async def _init_redis_stream() -> None:
    """Initialize Redis Stream dual-write if REDIS_URL is configured."""
    redis_url = os.getenv("REDIS_URL")
    if not redis_url:
        logger.info("REDIS_URL not set — EventBus running in memory-only mode")
        return

    try:
        import redis.asyncio as aioredis
        from src.core.redis_stream import RedisEventStream

        client = aioredis.from_url(redis_url, decode_responses=False)
        await client.ping()
        redis_stream = RedisEventStream(client)
        get_event_bus().enable_redis_stream(redis_stream)
        logger.info("Redis Stream dual-write enabled: %s", redis_url)
    except ImportError:
        logger.warning("redis package not installed — skipping Redis Stream")
    except Exception as e:
        logger.warning("Failed to connect to Redis — running memory-only: %s", e)


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Initialise database connections and EventBus on startup, clean up on shutdown."""
    logger.info("Starting LangGraph-compatible API server")
    await get_checkpointer()
    logger.info("Checkpointer ready")

    # Initialize EventBus + optional Redis Stream
    await _init_redis_stream()
    logger.info("EventBus ready")

    yield
    logger.info("Shutting down")
    await close_db()


def create_app() -> FastAPI:
    """Build and configure the FastAPI application."""
    application = FastAPI(
        title="LangGraph API",
        description="LangGraph Platform API compatible server for DeerFlow",
        version="0.1.0",
        lifespan=lifespan,
        docs_url="/docs",
        redoc_url="/redoc",
    )

    application.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    application.include_router(info_router)
    application.include_router(assistants_router)
    application.include_router(threads_router)
    application.include_router(thread_state_router)
    application.include_router(runs_router)

    return application


app = create_app()


if __name__ == "__main__":
    import uvicorn

    logging.basicConfig(level=logging.INFO)
    uvicorn.run(app, host="0.0.0.0", port=2024)
