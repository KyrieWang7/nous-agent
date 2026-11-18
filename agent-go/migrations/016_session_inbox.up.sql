CREATE TABLE agent_session_inbox (
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    position BIGSERIAL NOT NULL,
    delivery TEXT NOT NULL CHECK(delivery IN ('next_turn','next_step')),
    input JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','claimed','admitted','cancelled','rejected')),
    run_id TEXT REFERENCES agent_run(id) ON DELETE SET NULL,
    target_run_id TEXT,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    admitted_at TIMESTAMPTZ,
    PRIMARY KEY(thread_id,id)
);
CREATE INDEX agent_session_inbox_pending_idx ON agent_session_inbox(thread_id,position) WHERE status IN ('pending','claimed');
CREATE INDEX agent_session_inbox_run_idx ON agent_session_inbox(run_id);
