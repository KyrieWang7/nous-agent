ALTER TABLE agent_swarm_teams
    ADD COLUMN IF NOT EXISTS description TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS agent_swarm_team_lead_name_idx
    ON agent_swarm_teams(lead_thread_id, name);

ALTER TABLE agent_swarm_team_members
    ALTER COLUMN thread_id DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS model TEXT,
    ADD COLUMN IF NOT EXISTS prompt TEXT,
    ADD COLUMN IF NOT EXISTS joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
