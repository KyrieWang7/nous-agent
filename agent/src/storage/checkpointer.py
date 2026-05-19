"""Module-level checkpointer instance for ``langgraph_runtime_inmem``.

``langgraph_runtime_inmem`` expects a synchronously-available checkpointer
object at module-import time (via ``checkpoint.MEMORY``).  However,
``AsyncPostgresSaver`` requires an event loop, which doesn't exist yet at
import time.

This module provides a lightweight lazy proxy that:
1. Starts as a ``MemorySaver`` so the runtime can import it immediately.
2. On the first async call (``aget_tuple`` / ``aput`` / …) delegates to the
   real ``AsyncPostgresSaver`` initialised through ``database.get_checkpointer()``.
3. Exposes ``writes`` / ``storage`` / ``blobs`` for ``InMemorySaver`` compat.
"""

from __future__ import annotations

import logging
from typing import Any

from langgraph.checkpoint.memory import MemorySaver

from .database import get_checkpointer as _get_real_checkpointer

logger = logging.getLogger(__name__)


class _LazyPgCheckpointer(MemorySaver):
    """Synchronously importable proxy → async ``AsyncPostgresSaver``."""

    def __init__(self) -> None:
        super().__init__()
        self._real: Any | None = None
        self._init_attempted = False

    async def _ensure_real(self) -> None:
        if self._init_attempted:
            return
        self._init_attempted = True
        try:
            self._real = await _get_real_checkpointer()
            self._real.storage = self.storage
            self._real.writes = self.writes
            self._real.blobs = getattr(self, "blobs", {})
            logger.info("Lazy checkpointer upgraded to AsyncPostgresSaver")
        except Exception:
            logger.exception("Failed to init AsyncPostgresSaver — staying on MemorySaver")

    # ── async API proxied to the real saver ───────────────────────

    async def aget_tuple(self, config):
        await self._ensure_real()
        if self._real:
            return await self._real.aget_tuple(config)
        return await super().aget_tuple(config)

    async def aput(self, config, checkpoint, metadata, new_versions):
        await self._ensure_real()
        if self._real:
            return await self._real.aput(config, checkpoint, metadata, new_versions)
        return await super().aput(config, checkpoint, metadata, new_versions)

    async def aput_writes(self, config, writes, task_id, task_path=""):
        await self._ensure_real()
        if self._real:
            return await self._real.aput_writes(config, writes, task_id, task_path)
        return await super().aput_writes(config, writes, task_id, task_path)

    async def alist(self, config, *, filter=None, before=None, limit=None):
        await self._ensure_real()
        if self._real:
            async for item in self._real.alist(config, filter=filter, before=before, limit=limit):
                yield item
        else:
            async for item in super().alist(config, filter=filter, before=before, limit=limit):
                yield item

    async def __aenter__(self):
        await self._ensure_real()
        return self

    async def __aexit__(self, *args):
        pass


checkpointer = _LazyPgCheckpointer()
