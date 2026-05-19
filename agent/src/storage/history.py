"""Storage functions for deepagents conversation history offloading."""

import logging
from src.storage.database import get_db_connection

logger = logging.getLogger(__name__)

async def setup_history_tables() -> None:
    """Create the conversation_history table if it doesn't exist."""
    async with get_db_connection() as conn:
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS conversation_history (
                id SERIAL PRIMARY KEY,
                thread_id UUID UNIQUE NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
                content TEXT NOT NULL,
                updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
            );
            CREATE INDEX IF NOT EXISTS idx_conversation_history_thread_id 
                ON conversation_history(thread_id);
        """)
        logger.info("conversation_history table verified/created.")

async def write_conversation_history(thread_id: str, content: str) -> None:
    """Write or overwrite the conversation history for a thread."""
    async with get_db_connection() as conn:
        await conn.execute("""
            INSERT INTO conversation_history (thread_id, content) 
            VALUES ($1::uuid, $2)
            ON CONFLICT (thread_id) DO UPDATE 
            SET content = EXCLUDED.content, updated_at = CURRENT_TIMESTAMP;
        """, thread_id, content)

async def read_conversation_history(thread_id: str) -> str | None:
    """Read the conversation history for a thread."""
    async with get_db_connection() as conn:
        row = await conn.fetchrow("""
            SELECT content FROM conversation_history 
            WHERE thread_id = $1::uuid;
        """, thread_id)
        if row:
            return row["content"]
        return None
