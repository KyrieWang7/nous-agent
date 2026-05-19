"""PostgreSQL schema for the Swarm/Team system."""

import logging

from src.storage.database import get_db_connection

logger = logging.getLogger(__name__)

SWARM_TABLES_SQL = """
CREATE TABLE IF NOT EXISTS swarm_teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    lead_thread_id UUID NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS swarm_team_members (
    id SERIAL PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES swarm_teams(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    thread_id UUID,
    model VARCHAR(100),
    prompt TEXT,
    status VARCHAR(20) DEFAULT 'active',
    joined_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(team_id, name)
);

CREATE TABLE IF NOT EXISTS swarm_messages (
    id SERIAL PRIMARY KEY,
    team_id UUID NOT NULL REFERENCES swarm_teams(id) ON DELETE CASCADE,
    from_agent VARCHAR(100) NOT NULL,
    to_agent VARCHAR(100) NOT NULL,
    content TEXT NOT NULL,
    read BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_swarm_msg_to
    ON swarm_messages(team_id, to_agent, read);
CREATE INDEX IF NOT EXISTS idx_swarm_msg_team
    ON swarm_messages(team_id, created_at);
"""


async def setup_swarm_tables() -> None:
    """Create swarm tables if they don't exist."""
    async with get_db_connection() as conn:
        await conn.execute(SWARM_TABLES_SQL)
        logger.info("Swarm tables verified/created.")
