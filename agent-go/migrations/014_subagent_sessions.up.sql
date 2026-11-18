CREATE TABLE agent_subagent_task (
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    parent_run_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    result JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(thread_id, parent_run_id, task_id)
);
CREATE INDEX agent_subagent_task_id_idx ON agent_subagent_task(task_id);

CREATE TABLE agent_subagent_session (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    parent_session_id TEXT NOT NULL DEFAULT '',
    token TEXT NOT NULL,
    status TEXT NOT NULL,
    lease_until TIMESTAMPTZ NOT NULL,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX agent_subagent_session_thread_idx ON agent_subagent_session(thread_id, created_at);

CREATE TABLE agent_subagent_inbox (
    session_id TEXT NOT NULL REFERENCES agent_subagent_session(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    position BIGSERIAL NOT NULL,
    content TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','consumed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at TIMESTAMPTZ,
    PRIMARY KEY(session_id,id)
);
CREATE INDEX agent_subagent_inbox_pending_idx ON agent_subagent_inbox(session_id,position) WHERE status='pending';

CREATE TABLE agent_run_registry (
    run_id TEXT PRIMARY KEY REFERENCES agent_run(id) ON DELETE CASCADE,
    owner TEXT NOT NULL,
    payload JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX agent_run_registry_cancel_idx ON agent_run_registry(owner) WHERE cancel_requested;
