DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'agent_run_completion'
          AND column_name = 'auxiliary_tokens'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'agent_run_completion'
          AND column_name = 'middleware_tokens'
    ) THEN
        ALTER TABLE agent_run_completion
            RENAME COLUMN auxiliary_tokens TO middleware_tokens;
    END IF;
END $$;
