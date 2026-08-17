DROP INDEX IF EXISTS agent_run_event_idempotency_idx;

ALTER TABLE agent_run_event
    DROP COLUMN IF EXISTS idempotency_key;
