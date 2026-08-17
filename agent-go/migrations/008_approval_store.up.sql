CREATE TABLE IF NOT EXISTS agent_approval (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent_run(id) ON DELETE CASCADE,
    transaction_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    args JSONB NOT NULL DEFAULT '{}',
    reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    decision_by TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS agent_approval_run_status_idx
    ON agent_approval(run_id, status, updated_at DESC);
