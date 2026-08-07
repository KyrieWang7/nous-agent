"""baseline schema: app tables managed outside LangGraph checkpointer

Captures the application tables that nous-agent previously created ad-hoc via
``CREATE TABLE IF NOT EXISTS`` scattered across modules (session_manager,
history, event_store, swarm, memory). Written idempotently so it can be applied
to a fresh database OR stamped onto an existing one without conflict.

LangGraph checkpoint tables (``checkpoints*``) are intentionally NOT managed
here — they are owned by ``AsyncPostgresSaver.setup()``.

Revision ID: 0001_baseline_schema
Revises:
Create Date: 2026-06-11
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op

revision: str = "0001_baseline_schema"
down_revision: str | None = None
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


_UPGRADE_SQL = """
-- ---------------------------------------------------------------------------
-- Users + threads (storage/session_manager.py)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username VARCHAR(100) UNIQUE NOT NULL,
    display_name VARCHAR(200),
    email VARCHAR(255) UNIQUE,
    password_hash VARCHAR(255),
    avatar_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO users (id, username, display_name, email)
VALUES (
    '00000000-0000-0000-0000-000000000001'::uuid,
    'local-user',
    'Local User',
    'local-user@nous.local'
)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS threads (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    title TEXT,
    model_name TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    is_archived BOOLEAN DEFAULT FALSE
);

ALTER TABLE threads ADD COLUMN IF NOT EXISTS user_id UUID;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS title TEXT;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS model_name TEXT;
ALTER TABLE threads ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE threads ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();
ALTER TABLE threads ADD COLUMN IF NOT EXISTS is_archived BOOLEAN DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_threads_user_updated
    ON threads(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_threads_user_archived
    ON threads(user_id, is_archived);

-- ---------------------------------------------------------------------------
-- Conversation history offload (storage/history.py)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS conversation_history (
    id SERIAL PRIMARY KEY,
    thread_id UUID UNIQUE NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_conversation_history_thread_id
    ON conversation_history(thread_id);

-- ---------------------------------------------------------------------------
-- Run telemetry + token accounting (runtime/event_store.py)
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- Swarm / Team collaboration (swarm/schema.py)
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- Long-term memory (agents/memory/updater.py)
-- Previously relied on implicit/manual creation; codified here.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS user_memory_sections (
    user_id UUID NOT NULL,
    section_key TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, section_key)
);

CREATE TABLE IF NOT EXISTS user_memory_facts (
    id TEXT PRIMARY KEY,
    user_id UUID NOT NULL,
    content TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'context',
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    source TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_memory_facts_user
    ON user_memory_facts(user_id, created_at DESC);
"""


def upgrade() -> None:
    op.execute(_UPGRADE_SQL)


def downgrade() -> None:
    # Baseline migration: a downgrade would drop application data. Intentionally
    # a no-op so an accidental `downgrade base` cannot wipe the schema. Drop
    # tables manually if you truly need to reset.
    pass
