CREATE TABLE IF NOT EXISTS agent_projection_snapshot (
    run_id TEXT NOT NULL REFERENCES agent_run(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    last_seq BIGINT NOT NULL,
    messages JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (run_id, thread_id)
);
