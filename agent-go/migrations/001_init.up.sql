CREATE TABLE IF NOT EXISTS agent_thread (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    assistant_id TEXT NOT NULL DEFAULT 'lead_agent',
    model_name TEXT NOT NULL DEFAULT '',
    state JSONB NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_message (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    role TEXT NOT NULL,
    content JSONB NOT NULL,
    superseded BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (thread_id, seq)
);
CREATE INDEX IF NOT EXISTS agent_message_visible_idx ON agent_message(thread_id, superseded, seq);

CREATE TABLE IF NOT EXISTS agent_run (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    assistant_id TEXT NOT NULL DEFAULT 'lead_agent',
    status TEXT NOT NULL,
    on_disconnect TEXT NOT NULL DEFAULT 'cancel',
    model_name TEXT NOT NULL DEFAULT '',
    risk_level TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    duration_ms BIGINT
);
CREATE INDEX IF NOT EXISTS agent_run_thread_idx ON agent_run(thread_id, started_at DESC);

CREATE TABLE IF NOT EXISTS agent_run_event (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES agent_run(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    thread_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    category TEXT NOT NULL,
    content JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, seq)
);
CREATE INDEX IF NOT EXISTS agent_run_event_cursor_idx ON agent_run_event(run_id, seq);

CREATE TABLE IF NOT EXISTS agent_run_completion (
    run_id TEXT PRIMARY KEY REFERENCES agent_run(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL,
    status TEXT NOT NULL,
    iterations INTEGER NOT NULL DEFAULT 0,
    llm_call_count INTEGER NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    lead_tokens BIGINT NOT NULL DEFAULT 0,
    subagent_tokens BIGINT NOT NULL DEFAULT 0,
    middleware_tokens BIGINT NOT NULL DEFAULT 0,
    cost_micros BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memory_fact (
    id BIGSERIAL PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    fact TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS skill_usage (
    skill_name TEXT PRIMARY KEY,
    use_count BIGINT NOT NULL DEFAULT 0,
    view_count BIGINT NOT NULL DEFAULT 0,
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_swarm_teams (
    id TEXT PRIMARY KEY,
    lead_thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS agent_swarm_team_members (
	team_id TEXT NOT NULL REFERENCES agent_swarm_teams(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    thread_id TEXT NOT NULL REFERENCES agent_thread(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'idle',
    PRIMARY KEY (team_id, name)
);
CREATE TABLE IF NOT EXISTS agent_swarm_messages (
    id BIGSERIAL PRIMARY KEY,
	team_id TEXT NOT NULL REFERENCES agent_swarm_teams(id) ON DELETE CASCADE,
    from_agent TEXT NOT NULL,
    to_agent TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    read_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS agent_swarm_message_inbox_idx ON agent_swarm_messages(team_id, to_agent, read_at, id);
