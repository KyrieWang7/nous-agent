"""Alembic environment for nous-agent.

Migrations are raw SQL (``op.execute``) because the runtime uses raw asyncpg,
not the SQLAlchemy ORM. The database URL is resolved from ``LANGGRAPH_PG_URI``
(the same env var the app uses) and normalised to a synchronous psycopg driver
that Alembic can run migrations through.
"""

from __future__ import annotations

import os

from alembic import context
from sqlalchemy import engine_from_config, pool

config = context.config


def _resolve_sync_url() -> str:
    """Return a sync SQLAlchemy URL derived from LANGGRAPH_PG_URI.

    The app stores a plain ``postgresql://`` (asyncpg) DSN. Alembic runs
    synchronously, so we force the ``psycopg`` (v3) driver.
    """
    raw = os.environ.get("LANGGRAPH_PG_URI") or config.get_main_option("sqlalchemy.url") or ""
    if raw.startswith("postgresql+"):
        # Already has an explicit driver; trust it.
        return raw
    if raw.startswith("postgresql://"):
        return raw.replace("postgresql://", "postgresql+psycopg://", 1)
    if raw.startswith("postgres://"):
        return raw.replace("postgres://", "postgresql+psycopg://", 1)
    return raw


def run_migrations_offline() -> None:
    """Run migrations in 'offline' mode (emit SQL to stdout)."""
    url = _resolve_sync_url()
    context.configure(
        url=url,
        target_metadata=None,
        literal_binds=True,
        dialect_opts={"paramstyle": "named"},
    )
    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online() -> None:
    """Run migrations against a live database connection."""
    section = config.get_section(config.config_ini_section) or {}
    section["sqlalchemy.url"] = _resolve_sync_url()

    connectable = engine_from_config(
        section,
        prefix="sqlalchemy.",
        poolclass=pool.NullPool,
    )

    with connectable.connect() as connection:
        context.configure(connection=connection, target_metadata=None)
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
