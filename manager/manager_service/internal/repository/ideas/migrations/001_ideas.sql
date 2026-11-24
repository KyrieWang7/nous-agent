CREATE TABLE manager_ideas (
    id TEXT PRIMARY KEY CHECK (id <> ''),
    sequence BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    title TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('pending', 'doing', 'done')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
