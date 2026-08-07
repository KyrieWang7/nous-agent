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
CREATE INDEX IF NOT EXISTS agent_swarm_message_inbox_idx
    ON agent_swarm_messages(team_id, to_agent, read_at, id);

CREATE TABLE IF NOT EXISTS agent_swarm_message_receipts (
    message_id BIGINT NOT NULL REFERENCES agent_swarm_messages(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    read_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, agent_name)
);
