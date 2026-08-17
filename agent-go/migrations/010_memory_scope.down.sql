DROP INDEX IF EXISTS memory_fact_scope_unique_idx;
CREATE UNIQUE INDEX memory_fact_unique_idx ON memory_fact(thread_id, fact);

ALTER TABLE memory_fact
    DROP COLUMN IF EXISTS project_id,
    DROP COLUMN IF EXISTS user_id;
