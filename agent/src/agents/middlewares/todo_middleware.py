"""TodoMiddleware - context-loss detection and premature-exit prevention.

Production-grade todo management that solves two critical LLM failure modes:

1. Context-Loss Defense:
   When message history is truncated (e.g., by SummarizationMiddleware), the
   original `write_todos` tool call slides out of the context window. This
   middleware detects that situation in `before_model` and injects an invisible
   <system_reminder> so the model still knows about outstanding todos.

2. Premature-Exit Prevention:
   When the model produces a clean final answer (no tool calls) but todos are
   still incomplete, the middleware blocks the exit by:
   - Queuing a completion reminder for the next model call
   - Returning `{"jump_to": "model"}` to force re-engagement
   - The reminder lives only in the LLM request (via wrap_model_call),
     NOT in persisted message history — keeping user-visible streams clean.

   A retry cap (_MAX_COMPLETION_REMINDERS=2) prevents infinite loops when the
   agent truly cannot make further progress.
"""

from __future__ import annotations

import threading
from collections.abc import Awaitable, Callable
from typing import Any, override

from langchain.agents.middleware import TodoListMiddleware
from langchain.agents.middleware.todo import PlanningState, Todo
from langchain.agents.middleware.types import (
    ModelCallResult,
    ModelRequest,
    ModelResponse,
    hook_config,
)
from langchain_core.messages import AIMessage, HumanMessage
from langgraph.runtime import Runtime


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _todos_in_messages(messages: list[Any]) -> bool:
    """Return True if any AIMessage in messages contains a write_todos tool call."""
    for msg in messages:
        if isinstance(msg, AIMessage) and msg.tool_calls:
            for tc in msg.tool_calls:
                if tc.get("name") == "write_todos":
                    return True
    return False


def _reminder_in_messages(messages: list[Any]) -> bool:
    """Return True if a todo_reminder HumanMessage is already present."""
    for msg in messages:
        if isinstance(msg, HumanMessage) and getattr(msg, "name", None) == "todo_reminder":
            return True
    return False


def _format_todos(todos: list[Todo]) -> str:
    """Format a list of Todo items into a human-readable string."""
    lines: list[str] = []
    for todo in todos:
        status = todo.get("status", "pending")
        content = todo.get("content", "")
        lines.append(f"- [{status}] {content}")
    return "\n".join(lines)


def _format_completion_reminder(todos: list[Todo]) -> str:
    """Format a completion reminder for incomplete todo items."""
    incomplete = [t for t in todos if t.get("status") != "completed"]
    incomplete_text = "\n".join(
        f"- [{t.get('status', 'pending')}] {t.get('content', '')}" for t in incomplete
    )
    return (
        "<system_reminder>\n"
        "You have incomplete todo items that must be finished before giving your final response:\n\n"
        f"{incomplete_text}\n\n"
        "Please continue working on these tasks. Call `write_todos` to mark items as completed "
        "as you finish them, and only respond when all items are done.\n"
        "</system_reminder>"
    )


_TOOL_CALL_FINISH_REASONS = {"tool_calls", "function_call"}


def _has_tool_call_intent_or_error(message: AIMessage) -> bool:
    """Return True when an AIMessage is not a clean final answer.

    Checks all known locations for tool-call intent:
    - message.tool_calls (structured)
    - message.invalid_tool_calls (parse errors)
    - additional_kwargs (legacy/provider compat)
    - response_metadata.finish_reason (raw signal)
    """
    if message.tool_calls:
        return True

    if getattr(message, "invalid_tool_calls", None):
        return True

    additional_kwargs = getattr(message, "additional_kwargs", {}) or {}
    if additional_kwargs.get("tool_calls") or additional_kwargs.get("function_call"):
        return True

    response_metadata = getattr(message, "response_metadata", {}) or {}
    return response_metadata.get("finish_reason") in _TOOL_CALL_FINISH_REASONS


# ---------------------------------------------------------------------------
# Middleware
# ---------------------------------------------------------------------------


class TodoMiddleware(TodoListMiddleware):
    """Extends TodoListMiddleware with context-loss detection and premature-exit prevention.

    Context-Loss:
      In before_model, if todos exist but write_todos has left the context window,
      inject an invisible reminder so the model stays aware of outstanding work.

    Premature-Exit:
      In after_model, if the model outputs a clean final answer but todos are
      incomplete, queue a reminder and jump back to model. The reminder is
      injected via wrap_model_call (not persisted) to keep message history clean.
    """

    # Maximum completion reminders before allowing exit (prevents infinite loops)
    _MAX_COMPLETION_REMINDERS = 2
    # Hard cap for per-run reminder bookkeeping in long-lived middleware instances
    _MAX_COMPLETION_REMINDER_KEYS = 4096

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self._lock = threading.Lock()
        self._pending_completion_reminders: dict[tuple[str, str], list[str]] = {}
        self._completion_reminder_counts: dict[tuple[str, str], int] = {}
        self._completion_reminder_touch_order: dict[tuple[str, str], int] = {}
        self._completion_reminder_next_order = 0

    # ------------------------------------------------------------------
    # Context helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _get_thread_id(runtime: Runtime) -> str:
        context = getattr(runtime, "context", None)
        thread_id = context.get("thread_id") if context else None
        return str(thread_id) if thread_id else "default"

    @staticmethod
    def _get_run_id(runtime: Runtime) -> str:
        context = getattr(runtime, "context", None)
        run_id = context.get("run_id") if context else None
        return str(run_id) if run_id else "default"

    def _pending_key(self, runtime: Runtime) -> tuple[str, str]:
        return self._get_thread_id(runtime), self._get_run_id(runtime)

    # ------------------------------------------------------------------
    # Completion reminder state management (thread-safe)
    # ------------------------------------------------------------------

    def _touch_completion_reminder_key_locked(self, key: tuple[str, str]) -> None:
        self._completion_reminder_next_order += 1
        self._completion_reminder_touch_order[key] = self._completion_reminder_next_order

    def _completion_reminder_keys_locked(self) -> set[tuple[str, str]]:
        keys = set(self._pending_completion_reminders)
        keys.update(self._completion_reminder_counts)
        keys.update(self._completion_reminder_touch_order)
        return keys

    def _drop_completion_reminder_key_locked(self, key: tuple[str, str]) -> None:
        self._pending_completion_reminders.pop(key, None)
        self._completion_reminder_counts.pop(key, None)
        self._completion_reminder_touch_order.pop(key, None)

    def _prune_completion_reminder_state_locked(self, protected_key: tuple[str, str]) -> None:
        """LRU eviction for reminder state to prevent unbounded memory growth."""
        keys = self._completion_reminder_keys_locked()
        overflow = len(keys) - self._MAX_COMPLETION_REMINDER_KEYS
        if overflow <= 0:
            return
        candidates = [key for key in keys if key != protected_key]
        candidates.sort(key=lambda key: self._completion_reminder_touch_order.get(key, 0))
        for key in candidates[:overflow]:
            self._drop_completion_reminder_key_locked(key)

    def _queue_completion_reminder(self, runtime: Runtime, reminder: str) -> None:
        key = self._pending_key(runtime)
        with self._lock:
            self._pending_completion_reminders.setdefault(key, []).append(reminder)
            self._completion_reminder_counts[key] = (
                self._completion_reminder_counts.get(key, 0) + 1
            )
            self._touch_completion_reminder_key_locked(key)
            self._prune_completion_reminder_state_locked(protected_key=key)

    def _completion_reminder_count_for_runtime(self, runtime: Runtime) -> int:
        key = self._pending_key(runtime)
        with self._lock:
            return self._completion_reminder_counts.get(key, 0)

    def _drain_completion_reminders(self, runtime: Runtime) -> list[str]:
        key = self._pending_key(runtime)
        with self._lock:
            reminders = self._pending_completion_reminders.pop(key, [])
            if reminders or key in self._completion_reminder_counts:
                self._touch_completion_reminder_key_locked(key)
            return reminders

    def _clear_other_run_completion_reminders(self, runtime: Runtime) -> None:
        """Clear reminders from previous runs on the same thread."""
        thread_id, current_run_id = self._pending_key(runtime)
        with self._lock:
            for key in list(self._completion_reminder_keys_locked()):
                if key[0] == thread_id and key[1] != current_run_id:
                    self._drop_completion_reminder_key_locked(key)

    def _clear_current_run_completion_reminders(self, runtime: Runtime) -> None:
        key = self._pending_key(runtime)
        with self._lock:
            self._drop_completion_reminder_key_locked(key)

    # ------------------------------------------------------------------
    # before_agent: clean up stale reminders from previous runs
    # ------------------------------------------------------------------

    @override
    def before_agent(self, state: PlanningState, runtime: Runtime) -> dict[str, Any] | None:
        self._clear_other_run_completion_reminders(runtime)
        return None

    @override
    async def abefore_agent(
        self, state: PlanningState, runtime: Runtime
    ) -> dict[str, Any] | None:
        self._clear_other_run_completion_reminders(runtime)
        return None

    # ------------------------------------------------------------------
    # before_model: context-loss detection and reminder injection
    # ------------------------------------------------------------------

    @override
    def before_model(self, state: PlanningState, runtime: Runtime) -> dict[str, Any] | None:
        """Inject a todo-list reminder when write_todos has left the context window."""
        todos: list[Todo] = state.get("todos") or []  # type: ignore[assignment]
        if not todos:
            return None

        messages = state.get("messages") or []
        if _todos_in_messages(messages):
            # write_todos is still visible in context — nothing to do.
            return None

        if _reminder_in_messages(messages):
            # A reminder was already injected and hasn't been truncated yet.
            return None

        # The todo list exists in state but the original write_todos call is gone.
        # Inject a reminder as a HumanMessage so the model stays aware.
        formatted = _format_todos(todos)
        reminder = HumanMessage(
            name="todo_reminder",
            additional_kwargs={"hide_from_ui": True},
            content=(
                "<system_reminder>\n"
                "Your todo list from earlier is no longer visible in the current context window, "
                "but it is still active. Here is the current state:\n\n"
                f"{formatted}\n\n"
                "Continue tracking and updating this todo list as you work. "
                "Call `write_todos` whenever the status of any item changes.\n"
                "</system_reminder>"
            ),
        )
        return {"messages": [reminder]}

    @override
    async def abefore_model(
        self, state: PlanningState, runtime: Runtime
    ) -> dict[str, Any] | None:
        return self.before_model(state, runtime)

    # ------------------------------------------------------------------
    # after_model: premature-exit prevention
    # ------------------------------------------------------------------

    @hook_config(can_jump_to=["model"])
    @override
    def after_model(self, state: PlanningState, runtime: Runtime) -> dict[str, Any] | None:
        """Prevent premature agent exit when todo items are still incomplete.

        Strategy:
        1. Preserve base class logic (parallel write_todos detection).
        2. Only intervene when model produces a clean final answer (no tool calls).
        3. Allow exit when all todos are completed.
        4. Enforce reminder cap to prevent infinite loops.
        5. Queue reminder for next model request and jump back.
        """
        # 1. Preserve base class logic
        base_result = super().after_model(state, runtime)
        if base_result is not None:
            return base_result

        # 2. Only intervene on clean final answers
        messages = state.get("messages") or []
        last_ai = next((m for m in reversed(messages) if isinstance(m, AIMessage)), None)
        if not last_ai or _has_tool_call_intent_or_error(last_ai):
            return None

        # 3. Allow exit when all todos are completed
        todos: list[Todo] = state.get("todos") or []  # type: ignore[assignment]
        if not todos or all(t.get("status") == "completed" for t in todos):
            return None

        # 4. Enforce reminder cap
        if self._completion_reminder_count_for_runtime(runtime) >= self._MAX_COMPLETION_REMINDERS:
            return None

        # 5. Queue reminder and jump back to model
        self._queue_completion_reminder(runtime, _format_completion_reminder(todos))
        return {"jump_to": "model"}

    @override
    @hook_config(can_jump_to=["model"])
    async def aafter_model(
        self, state: PlanningState, runtime: Runtime
    ) -> dict[str, Any] | None:
        return self.after_model(state, runtime)

    # ------------------------------------------------------------------
    # wrap_model_call: inject completion reminders into LLM request
    # ------------------------------------------------------------------

    @staticmethod
    def _format_pending_completion_reminders(reminders: list[str]) -> str:
        """Deduplicate and join pending reminders."""
        return "\n\n".join(dict.fromkeys(reminders))

    def _augment_request(self, request: ModelRequest) -> ModelRequest:
        """Append queued completion reminders to the model request.

        The reminder lives ONLY in the LLM request — it is NOT persisted
        to message history, keeping user-visible message streams clean.
        """
        reminders = self._drain_completion_reminders(request.runtime)
        if not reminders:
            return request
        new_messages = [
            *request.messages,
            HumanMessage(
                content=self._format_pending_completion_reminders(reminders),
                name="todo_completion_reminder",
                additional_kwargs={"hide_from_ui": True},
            ),
        ]
        return request.override(messages=new_messages)

    @override
    def wrap_model_call(
        self,
        request: ModelRequest,
        handler: Callable[[ModelRequest], ModelResponse],
    ) -> ModelCallResult:
        return handler(self._augment_request(request))

    @override
    async def awrap_model_call(
        self,
        request: ModelRequest,
        handler: Callable[[ModelRequest], Awaitable[ModelResponse]],
    ) -> ModelCallResult:
        return await handler(self._augment_request(request))

    # ------------------------------------------------------------------
    # after_agent: cleanup
    # ------------------------------------------------------------------

    @override
    def after_agent(self, state: PlanningState, runtime: Runtime) -> dict[str, Any] | None:
        self._clear_current_run_completion_reminders(runtime)
        return None

    @override
    async def aafter_agent(
        self, state: PlanningState, runtime: Runtime
    ) -> dict[str, Any] | None:
        self._clear_current_run_completion_reminders(runtime)
        return None
