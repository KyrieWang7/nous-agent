ALTER TABLE memory_fact
    ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS project_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS memory_fact_unique_idx;
CREATE UNIQUE INDEX memory_fact_scope_unique_idx
    ON memory_fact(user_id, project_id, thread_id, fact);
