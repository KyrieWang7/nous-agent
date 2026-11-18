CREATE TABLE IF NOT EXISTS agent_schedule (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    schedule_type TEXT NOT NULL,
    expression TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    input JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    next_run_at TIMESTAMPTZ NOT NULL,
    last_run_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS agent_schedule_due_idx ON agent_schedule(enabled, next_run_at);
