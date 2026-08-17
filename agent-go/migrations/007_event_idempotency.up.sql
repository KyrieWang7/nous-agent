ALTER TABLE agent_run_event
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS agent_run_event_idempotency_idx
    ON agent_run_event(run_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';
