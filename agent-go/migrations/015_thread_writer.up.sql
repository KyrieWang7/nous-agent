CREATE TABLE agent_thread_writer (
    thread_id TEXT PRIMARY KEY REFERENCES agent_thread(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES agent_run(id) ON DELETE CASCADE
);
