DELETE FROM agent_swarm_team_members WHERE thread_id IS NULL;

ALTER TABLE agent_swarm_team_members
    ALTER COLUMN thread_id SET NOT NULL,
    DROP COLUMN IF EXISTS joined_at,
    DROP COLUMN IF EXISTS prompt,
    DROP COLUMN IF EXISTS model;

DROP INDEX IF EXISTS agent_swarm_team_lead_name_idx;

ALTER TABLE agent_swarm_teams
    DROP COLUMN IF EXISTS description;
