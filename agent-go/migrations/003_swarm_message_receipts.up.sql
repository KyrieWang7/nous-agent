CREATE TABLE IF NOT EXISTS agent_swarm_message_receipts (
    message_id BIGINT NOT NULL REFERENCES agent_swarm_messages(id) ON DELETE CASCADE,
    agent_name TEXT NOT NULL,
    read_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, agent_name)
);

-- Preserve the consumed state of legacy direct messages. A legacy broadcast
-- cannot identify which member consumed it, so it is deliberately delivered
-- once to every member after this migration.
INSERT INTO agent_swarm_message_receipts(message_id, agent_name, read_at)
SELECT id, to_agent, read_at
FROM agent_swarm_messages
WHERE read_at IS NOT NULL AND to_agent <> '*'
ON CONFLICT DO NOTHING;
