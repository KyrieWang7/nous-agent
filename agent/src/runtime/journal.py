"""Run event capture via LangChain callbacks with bucketed token accounting.

RunJournal sits between LangChain's callback mechanism and the pluggable
RunEventStore. It standardizes callback data into run event records and
handles token usage accumulation with caller-based bucketing.

Key design decisions:
- on_chat_model_start captures the first human message (most reliable source,
  not affected by context trimming)
- on_llm_end accumulates tokens and routes them to caller-specific buckets
  based on tags (lead_agent / subagent:{name} / middleware:{name})
- Dedup by LangChain run_id prevents double-counting from repeated callbacks
- Async batch flush with buffer prevents blocking the agent execution loop

Token Bucketing:
- lead_agent_tokens: Main decision-making agent's LLM consumption
- subagent_tokens: Vertical sub-agents (web search, code writer, etc.)
- middleware_tokens: System overhead (title generation, summarization, etc.)
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import Mapping
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any, cast
from uuid import UUID

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, ToolMessage

if TYPE_CHECKING:
    from langgraph.types import Command

    from src.runtime.event_store import RunEventStore

logger = logging.getLogger(__name__)


class RunJournal(BaseCallbackHandler):
    """LangChain callback handler for telemetry capture and token bucketing.

    Captures LLM requests/responses, tool invocations, and accumulates
    token usage with per-caller bucketing for cost attribution.

    Usage:
        journal = RunJournal(run_id, thread_id, event_store)
        config = {"callbacks": [journal]}
        await graph.astream(input, config=config)
        # After completion:
        completion_data = journal.get_completion_data()
        await event_store.save_completion(run_id, thread_id, completion_data)
    """

    def __init__(
        self,
        run_id: str,
        thread_id: str,
        event_store: RunEventStore,
        *,
        track_token_usage: bool = True,
        flush_threshold: int = 20,
    ):
        """Initialize the journal.

        Args:
            run_id: Unique identifier for this agent run.
            thread_id: The conversation thread this run belongs to.
            event_store: Backend for persisting captured events.
            track_token_usage: Whether to accumulate token counts.
            flush_threshold: Number of buffered events before auto-flush.
        """
        super().__init__()
        self.run_id = run_id
        self.thread_id = thread_id
        self._store = event_store
        self._track_tokens = track_token_usage
        self._flush_threshold = flush_threshold

        # Write buffer
        self._buffer: list[dict] = []
        self._pending_flush_tasks: set[asyncio.Task[None]] = set()

        # Token accumulators
        self._total_input_tokens = 0
        self._total_output_tokens = 0
        self._total_tokens = 0
        self._llm_call_count = 0

        # Caller-bucketed token accumulators
        self._lead_agent_tokens = 0
        self._subagent_tokens = 0
        self._middleware_tokens = 0

        # Dedup: LangChain may fire on_llm_end multiple times for the same run_id
        self._counted_llm_run_ids: set[str] = set()
        self._counted_external_source_ids: set[str] = set()
        self._counted_message_llm_run_ids: set[str] = set()

        # Convenience fields
        self._last_ai_msg: str | None = None
        self._first_human_msg: str | None = None
        self._msg_count = 0

        # Latency tracking
        self._llm_start_times: dict[str, float] = {}

        # LLM call indexing
        self._llm_call_index = 0
        self._seen_llm_starts: set[str] = set()

        # Timing
        self._started_at = time.monotonic()

    # ------------------------------------------------------------------
    # Message helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _message_text(message: BaseMessage) -> str:
        """Extract displayable text from a message's mixed content shape."""
        content = getattr(message, "content", None)
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            parts: list[str] = []
            for block in content:
                if isinstance(block, str):
                    parts.append(block)
                elif isinstance(block, Mapping):
                    text = block.get("text")
                    if isinstance(text, str):
                        parts.append(text)
                    else:
                        nested = block.get("content")
                        if isinstance(nested, str):
                            parts.append(nested)
            return "".join(parts)
        if isinstance(content, Mapping):
            for key in ("text", "content"):
                value = content.get(key)
                if isinstance(value, str):
                    return value
        text = getattr(message, "text", None)
        if isinstance(text, str):
            return text
        return ""

    def _record_message_summary(self, message: BaseMessage, *, caller: str | None = None) -> None:
        """Update run-level convenience fields for persisted run rows."""
        self._msg_count += 1

        is_ai_message = isinstance(message, AIMessage) or getattr(message, "type", None) == "ai"
        if is_ai_message and (caller is None or caller == "lead_agent"):
            text = self._message_text(message).strip()
            if text:
                self._last_ai_msg = text[:2000]

    # ------------------------------------------------------------------
    # Caller identification
    # ------------------------------------------------------------------

    def _identify_caller(self, tags: list[str] | None) -> str:
        """Identify the caller from LangChain tags.

        Tags are injected by the agent framework:
        - "lead_agent" — the main orchestrator
        - "subagent:{name}" — vertical sub-agents
        - "middleware:{name}" — system middleware (title gen, summarize, etc.)

        Default is "lead_agent" when no matching tag is found.
        """
        for tag in (tags or []):
            if isinstance(tag, str) and (
                tag.startswith("subagent:") or tag.startswith("middleware:") or tag == "lead_agent"
            ):
                return tag
        return "lead_agent"

    # ------------------------------------------------------------------
    # Chain lifecycle callbacks
    # ------------------------------------------------------------------

    def on_chain_start(
        self,
        serialized: dict[str, Any],
        inputs: dict[str, Any],
        *,
        run_id: UUID,
        parent_run_id: UUID | None = None,
        tags: list[str] | None = None,
        metadata: dict[str, Any] | None = None,
        **kwargs: Any,
    ) -> None:
        caller = self._identify_caller(tags)
        if parent_run_id is None:
            chain_name = (serialized or {}).get("name", "unknown")
            self._put(
                event_type="run.start",
                category="trace",
                content={"chain": chain_name},
                metadata={"caller": caller, **(metadata or {})},
            )

    def on_chain_end(self, outputs: Any, *, run_id: UUID, **kwargs: Any) -> None:
        self._put(event_type="run.end", category="outputs", content={}, metadata={"status": "success"})
        self._flush_sync()

    def on_chain_error(self, error: BaseException, *, run_id: UUID, **kwargs: Any) -> None:
        self._put(
            event_type="run.error",
            category="error",
            content=str(error),
            metadata={"error_type": type(error).__name__},
        )
        self._flush_sync()

    # ------------------------------------------------------------------
    # LLM callbacks — the core of token bucketing
    # ------------------------------------------------------------------

    def on_chat_model_start(
        self,
        serialized: dict,
        messages: list[list[BaseMessage]],
        *,
        run_id: UUID,
        tags: list[str] | None = None,
        **kwargs: Any,
    ) -> None:
        """Capture structured prompt and extract first human message.

        This is the canonical place to extract the first human message:
        messages are fully structured here, it fires only on real LLM calls,
        and the content is never compressed by checkpoint trimming.
        """
        rid = str(run_id)
        self._llm_start_times[rid] = time.monotonic()
        self._llm_call_index += 1
        self._seen_llm_starts.add(rid)

        # Extract first human message (walk messages in reverse for efficiency)
        if not self._first_human_msg and messages:
            for batch in reversed(messages):
                for m in reversed(batch):
                    if isinstance(m, HumanMessage) and getattr(m, "name", None) != "summary":
                        caller = self._identify_caller(tags)
                        text = self._message_text(m)
                        self.set_first_human_message(text)
                        self._put(
                            event_type="llm.human.input",
                            category="message",
                            content={"text": text[:2000]},
                            metadata={"caller": caller},
                        )
                        self._record_message_summary(m, caller=caller)
                        break
                if self._first_human_msg:
                    break

    def on_llm_start(
        self,
        serialized: dict,
        prompts: list[str],
        *,
        run_id: UUID,
        parent_run_id: UUID | None = None,
        tags: list[str] | None = None,
        metadata: dict[str, Any] | None = None,
        **kwargs: Any,
    ) -> None:
        """Fallback for non-chat models. Just track latency."""
        self._llm_start_times[str(run_id)] = time.monotonic()

    def on_llm_end(
        self,
        response: Any,
        *,
        run_id: UUID,
        parent_run_id: UUID | None = None,
        tags: list[str] | None = None,
        **kwargs: Any,
    ) -> None:
        """Accumulate token usage and route to caller-specific buckets.

        This is where the billing magic happens: each LLM response carries
        usage_metadata, and we route the token counts to the appropriate
        bucket based on the caller tag.
        """
        messages: list[BaseMessage] = []
        for generation in response.generations:
            for gen in generation:
                if hasattr(gen, "message"):
                    messages.append(gen.message)

        for message in messages:
            caller = self._identify_caller(tags)
            rid = str(run_id)

            # Latency
            start = self._llm_start_times.pop(rid, None)
            latency_ms = int((time.monotonic() - start) * 1000) if start else None

            # Token usage from message
            usage = getattr(message, "usage_metadata", None)
            usage_dict = dict(usage) if usage else {}

            # Resolve call index
            call_index = self._llm_call_index
            if rid not in self._seen_llm_starts:
                self._llm_call_index += 1
                call_index = self._llm_call_index
                self._seen_llm_starts.add(rid)

            # Emit event
            self._put(
                event_type="llm.ai.response",
                category="message",
                content={"text": self._message_text(message)[:1000]},
                metadata={
                    "caller": caller,
                    "usage": usage_dict,
                    "latency_ms": latency_ms,
                    "llm_call_index": call_index,
                },
            )

            if rid not in self._counted_message_llm_run_ids:
                self._record_message_summary(message, caller=caller)

            # Token accumulation with bucketing
            if self._track_tokens:
                input_tk = usage_dict.get("input_tokens", 0) or 0
                output_tk = usage_dict.get("output_tokens", 0) or 0
                total_tk = usage_dict.get("total_tokens", 0) or 0
                if total_tk == 0:
                    total_tk = input_tk + output_tk

                if total_tk > 0 and rid not in self._counted_llm_run_ids:
                    self._counted_llm_run_ids.add(rid)
                    self._total_input_tokens += input_tk
                    self._total_output_tokens += output_tk
                    self._total_tokens += total_tk
                    self._llm_call_count += 1

                    # Route to caller bucket
                    if caller.startswith("subagent:"):
                        self._subagent_tokens += total_tk
                    elif caller.startswith("middleware:"):
                        self._middleware_tokens += total_tk
                    else:
                        self._lead_agent_tokens += total_tk

        if messages:
            self._counted_message_llm_run_ids.add(str(run_id))

    def on_llm_error(self, error: BaseException, *, run_id: UUID, **kwargs: Any) -> None:
        self._llm_start_times.pop(str(run_id), None)
        self._put(event_type="llm.error", category="trace", content=str(error))

    # ------------------------------------------------------------------
    # Tool callbacks
    # ------------------------------------------------------------------

    def on_tool_start(
        self, serialized: Any, input_str: Any, *, run_id: UUID, **kwargs: Any
    ) -> None:
        tool_name = (serialized or {}).get("name", "unknown") if isinstance(serialized, dict) else "unknown"
        self._put(
            event_type="tool.start",
            category="trace",
            content={"tool": tool_name},
            metadata={"run_id": str(run_id)},
        )

    def on_tool_end(self, output: Any, *, run_id: UUID, **kwargs: Any) -> None:
        try:
            if isinstance(output, ToolMessage):
                self._put(
                    event_type="llm.tool.result",
                    category="message",
                    content={"tool_call_id": getattr(output, "tool_call_id", None)},
                )
                self._record_message_summary(output)
            else:
                # Handle Command outputs from LangGraph
                from langgraph.types import Command
                if isinstance(output, Command):
                    cmd_messages = output.update.get("messages", [])
                    for msg in cmd_messages:
                        if isinstance(msg, BaseMessage):
                            self._put(
                                event_type="llm.tool.result",
                                category="message",
                                content={},
                            )
                            self._record_message_summary(msg)
        except Exception:
            logger.debug("on_tool_end: failed to process output", exc_info=True)

    # ------------------------------------------------------------------
    # External usage records (from sub-agents with own LLM calls)
    # ------------------------------------------------------------------

    def record_external_llm_usage(self, records: list[dict[str, int | str]]) -> None:
        """Record token usage from external sources (e.g., autonomous sub-agents).

        Each record should contain:
            source_run_id: Unique identifier to prevent double-counting
            caller: Caller tag (e.g. "subagent:code-writer")
            input_tokens, output_tokens, total_tokens
        """
        if not self._track_tokens:
            return
        for record in records:
            source_id = str(record.get("source_run_id", ""))
            if not source_id or source_id in self._counted_external_source_ids:
                continue

            total_tk = record.get("total_tokens", 0) or 0
            if total_tk <= 0:
                input_tk = record.get("input_tokens", 0) or 0
                output_tk = record.get("output_tokens", 0) or 0
                total_tk = input_tk + output_tk
            if total_tk <= 0:
                continue

            self._counted_external_source_ids.add(source_id)
            self._total_input_tokens += record.get("input_tokens", 0) or 0
            self._total_output_tokens += record.get("output_tokens", 0) or 0
            self._total_tokens += total_tk

            caller = str(record.get("caller", ""))
            if caller.startswith("subagent:"):
                self._subagent_tokens += total_tk
            elif caller.startswith("middleware:"):
                self._middleware_tokens += total_tk
            else:
                self._lead_agent_tokens += total_tk

    # ------------------------------------------------------------------
    # Middleware recording
    # ------------------------------------------------------------------

    def record_middleware(
        self, tag: str, *, name: str, hook: str, action: str, changes: dict
    ) -> None:
        """Record a middleware state-change event.

        Args:
            tag: Short identifier (e.g., "title", "summarize").
            name: Full middleware class name.
            hook: Lifecycle hook that triggered the action.
            action: Specific action performed.
            changes: Dict describing the state changes made.
        """
        self._put(
            event_type=f"middleware:{tag}",
            category="middleware",
            content={"name": name, "hook": hook, "action": action, "changes": changes},
        )

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def set_first_human_message(self, content: str) -> None:
        """Record the first human message for convenience fields."""
        self._first_human_msg = content[:2000] if content else None

    def get_completion_data(self) -> dict[str, Any]:
        """Return accumulated token and message data for run completion.

        This is the core output of the billing system — provides bucketed
        costs for lead_agent, subagents, and middleware.
        """
        duration_ms = int((time.monotonic() - self._started_at) * 1000)
        return {
            "total_input_tokens": self._total_input_tokens,
            "total_output_tokens": self._total_output_tokens,
            "total_tokens": self._total_tokens,
            "llm_call_count": self._llm_call_count,
            "lead_agent_tokens": self._lead_agent_tokens,
            "subagent_tokens": self._subagent_tokens,
            "middleware_tokens": self._middleware_tokens,
            "message_count": self._msg_count,
            "last_ai_message": self._last_ai_msg,
            "first_human_message": self._first_human_msg,
            "duration_ms": duration_ms,
        }

    async def flush(self) -> None:
        """Force flush remaining buffer. Called in worker's finally block."""
        # Wait for in-flight flushes
        if self._pending_flush_tasks:
            await asyncio.gather(*tuple(self._pending_flush_tasks), return_exceptions=True)

        # Flush remaining buffer
        while self._buffer:
            batch = self._buffer[: self._flush_threshold]
            del self._buffer[: self._flush_threshold]
            try:
                await self._store.put_batch(batch)
            except Exception:
                self._buffer = batch + self._buffer
                logger.warning("Failed to flush journal buffer", exc_info=True)
                raise

    # ------------------------------------------------------------------
    # Internal buffer management
    # ------------------------------------------------------------------

    def _put(
        self,
        *,
        event_type: str,
        category: str,
        content: str | dict = "",
        metadata: dict | None = None,
    ) -> None:
        """Buffer an event for async persistence."""
        self._buffer.append(
            {
                "thread_id": self.thread_id,
                "run_id": self.run_id,
                "event_type": event_type,
                "category": category,
                "content": content if isinstance(content, dict) else {"text": content},
                "metadata": metadata or {},
                # Store a tz-aware datetime (not an ISO string): asyncpg's binary
                # protocol encodes the parameter before the SQL `::timestamptz`
                # cast applies, so a str would raise DataError.
                "created_at": datetime.now(UTC),
            }
        )
        if len(self._buffer) >= self._flush_threshold:
            self._flush_sync()

    def _flush_sync(self) -> None:
        """Best-effort flush from synchronous callback context.

        If an event loop is running, schedule async flush. Otherwise
        events stay buffered for the explicit flush() call.
        """
        if not self._buffer:
            return
        if self._pending_flush_tasks:
            return
        try:
            loop = asyncio.get_running_loop()
        except RuntimeError:
            return
        batch = self._buffer.copy()
        self._buffer.clear()
        task = loop.create_task(self._flush_async(batch))
        self._pending_flush_tasks.add(task)
        task.add_done_callback(self._on_flush_done)

    async def _flush_async(self, batch: list[dict]) -> None:
        try:
            await self._store.put_batch(batch)
        except Exception:
            logger.warning(
                "Failed to flush %d events for run %s — returning to buffer",
                len(batch),
                self.run_id,
                exc_info=True,
            )
            self._buffer = batch + self._buffer

    def _on_flush_done(self, task: asyncio.Task) -> None:
        self._pending_flush_tasks.discard(task)
        if task.cancelled():
            return
        exc = task.exception()
        if exc:
            logger.warning("Journal flush task failed: %s", exc)
