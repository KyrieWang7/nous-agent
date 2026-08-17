CREATE TABLE IF NOT EXISTS agent_question (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent_run(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    tool_call_id TEXT NOT NULL DEFAULT '',
    header TEXT NOT NULL,
    question TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    options JSONB NOT NULL DEFAULT '[]',
    intent TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    answer JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    answered_by TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS agent_question_run_status_idx
    ON agent_question(run_id, status, updated_at DESC);

CREATE INDEX IF NOT EXISTS agent_question_thread_status_idx
    ON agent_question(thread_id, status, updated_at DESC);
