# Database Migrations (Alembic)

nous-agent uses **raw asyncpg** at runtime, so these Alembic migrations are
intentionally written as **raw SQL** (`op.execute(...)`) rather than
SQLAlchemy-ORM autogenerate. Alembic provides ordered, versioned, idempotent
schema management; the runtime keeps using plain asyncpg.

## Scope

Managed here (application tables):

- `users`, `threads`
- `conversation_history`
- `run_events`, `run_completions`
- `swarm_teams`, `swarm_team_members`, `swarm_messages`
- `user_memory_sections`, `user_memory_facts`

**Not** managed here:

- LangGraph checkpoint tables (`checkpoints*`) — owned by
  `AsyncPostgresSaver.setup()` in `src/storage/database.py`.

## Commands

The database URL is read from the `LANGGRAPH_PG_URI` environment variable (the
same one the app uses); `env.py` normalises it to a sync `psycopg` driver.

```bash
make migrate            # alembic upgrade head
make migrate-current    # show current revision
make migrate-new m="add foo table"   # scaffold a new migration
make migrate-down       # downgrade one step

# or directly:
cd agent && uv run alembic upgrade head
cd agent && uv run alembic upgrade head --sql   # offline: print SQL, no DB needed
```

## Baseline & existing deployments

`0001_baseline_schema` captures the pre-existing schema using
`CREATE TABLE IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`, so it is safe to run
against either a fresh database or one that already has the tables (e.g. created
by the legacy in-code `setup_*` functions). For an existing deployment, either:

- run `make migrate` (the baseline is idempotent and will simply stamp the
  version), or
- stamp without executing DDL: `cd agent && uv run alembic stamp 0001_baseline_schema`.

## Authoring new migrations

1. `make migrate-new m="describe change"`
2. Edit the generated file under `migrations/versions/`; put DDL inside
   `op.execute("""... SQL ...""")`. Prefer idempotent statements
   (`IF NOT EXISTS`) where practical.
3. Provide a real `downgrade()` for reversible changes (the baseline's downgrade
   is intentionally a no-op to avoid accidental data loss).
4. Run `make migrate` and verify with `make migrate-current`.

## Relationship to in-code `setup_*` functions

The legacy `setup_threads_table()`, `setup_swarm_tables()`,
`setup_history_tables()`, and event-store `setup()` remain (idempotent) for
backward compatibility and local dev. Alembic is the source of truth for schema
evolution going forward — add new columns/tables as migrations, not as new
`CREATE TABLE IF NOT EXISTS` blocks scattered across modules.
