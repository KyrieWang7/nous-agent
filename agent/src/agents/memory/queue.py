"""Memory update queue with debounce mechanism.

Enhanced with DeerFlow features:
- Carries correction_detected / reinforcement_detected signals
- Uses ThreadPoolExecutor instead of asyncio.run() to avoid cross-loop bugs
- Merges signals when replacing existing queue entry for same thread
"""

import concurrent.futures
import logging
import threading
import time
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

from src.config.memory_config import get_memory_config

logger = logging.getLogger(__name__)


@dataclass
class ConversationContext:
    """Context for a conversation to be processed for memory update."""

    thread_id: str
    messages: list[Any]
    timestamp: datetime = field(default_factory=lambda: datetime.now(UTC))
    correction_detected: bool = False
    reinforcement_detected: bool = False


# Thread pool for memory update processing — avoids creating new event loops
_MEMORY_EXECUTOR = concurrent.futures.ThreadPoolExecutor(
    max_workers=2,
    thread_name_prefix="memory-updater",
)


class MemoryUpdateQueue:
    """Queue for memory updates with debounce mechanism.

    This queue collects conversation contexts and processes them after
    a configurable debounce period. Multiple conversations received within
    the debounce window are batched together.
    """

    def __init__(self):
        """Initialize the memory update queue."""
        self._queue: list[ConversationContext] = []
        self._lock = threading.Lock()
        self._timer: threading.Timer | None = None
        self._processing = False

    def add(
        self,
        thread_id: str,
        messages: list[Any],
        correction_detected: bool = False,
        reinforcement_detected: bool = False,
    ) -> None:
        """Add a conversation to the update queue.

        Args:
            thread_id: The thread ID.
            messages: The conversation messages (already filtered).
            correction_detected: Whether user correction was detected.
            reinforcement_detected: Whether positive reinforcement was detected.
        """
        config = get_memory_config()
        if not config.enabled:
            return

        with self._lock:
            # Merge signals with existing entry for same thread
            existing = next((c for c in self._queue if c.thread_id == thread_id), None)
            merged_correction = correction_detected or (existing.correction_detected if existing else False)
            merged_reinforcement = reinforcement_detected or (existing.reinforcement_detected if existing else False)

            context = ConversationContext(
                thread_id=thread_id,
                messages=messages,
                correction_detected=merged_correction,
                reinforcement_detected=merged_reinforcement,
            )

            # Replace existing entry for same thread
            self._queue = [c for c in self._queue if c.thread_id != thread_id]
            self._queue.append(context)

            # Reset or start the debounce timer
            self._reset_timer()

        logger.info("Memory update queued for thread %s, queue size: %d", thread_id, len(self._queue))

    def _reset_timer(self) -> None:
        """Reset the debounce timer."""
        config = get_memory_config()

        # Cancel existing timer if any
        if self._timer is not None:
            self._timer.cancel()

        # Start new timer
        self._timer = threading.Timer(
            config.debounce_seconds,
            self._process_queue,
        )
        self._timer.daemon = True
        self._timer.start()

        logger.debug("Memory update timer set for %ds", config.debounce_seconds)

    def _process_queue(self) -> None:
        """Process all queued conversation contexts.

        Uses ThreadPoolExecutor with sync model.invoke() to avoid
        cross-loop httpx connection pool bugs (DeerFlow issue #2615).
        """
        from src.agents.memory.updater import MemoryUpdater

        with self._lock:
            if self._processing:
                self._reset_timer()
                return

            if not self._queue:
                return

            self._processing = True
            contexts_to_process = self._queue.copy()
            self._queue.clear()
            self._timer = None

        logger.info("Processing %d queued memory updates", len(contexts_to_process))

        try:
            updater = MemoryUpdater()

            for context in contexts_to_process:
                try:
                    logger.info("Updating memory for thread %s", context.thread_id)
                    # Use the sync path (model.invoke) which doesn't touch async pools
                    future = _MEMORY_EXECUTOR.submit(
                        updater.update_memory_sync,
                        messages=context.messages,
                        thread_id=context.thread_id,
                        correction_detected=context.correction_detected,
                        reinforcement_detected=context.reinforcement_detected,
                    )
                    success = future.result(timeout=120)
                    if success:
                        logger.info("Memory updated for thread %s", context.thread_id)
                    else:
                        logger.info("Memory update skipped for thread %s", context.thread_id)
                except Exception:
                    logger.error("Error updating memory for thread %s", context.thread_id, exc_info=True)

                if len(contexts_to_process) > 1:
                    time.sleep(0.5)

        finally:
            with self._lock:
                self._processing = False

    def flush(self) -> None:
        """Force immediate processing of the queue."""
        with self._lock:
            if self._timer is not None:
                self._timer.cancel()
                self._timer = None

        self._process_queue()

    def clear(self) -> None:
        """Clear the queue without processing."""
        with self._lock:
            if self._timer is not None:
                self._timer.cancel()
                self._timer = None
            self._queue.clear()
            self._processing = False

    @property
    def pending_count(self) -> int:
        """Get the number of pending updates."""
        with self._lock:
            return len(self._queue)

    @property
    def is_processing(self) -> bool:
        """Check if the queue is currently being processed."""
        with self._lock:
            return self._processing


# Global singleton instance
_memory_queue: MemoryUpdateQueue | None = None
_queue_lock = threading.Lock()


def get_memory_queue() -> MemoryUpdateQueue:
    """Get the global memory update queue singleton."""
    global _memory_queue
    with _queue_lock:
        if _memory_queue is None:
            _memory_queue = MemoryUpdateQueue()
        return _memory_queue


def reset_memory_queue() -> None:
    """Reset the global memory queue (testing only)."""
    global _memory_queue
    with _queue_lock:
        if _memory_queue is not None:
            _memory_queue.clear()
        _memory_queue = None
