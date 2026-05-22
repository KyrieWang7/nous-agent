"""Middleware to detect and break repetitive tool call loops.

Production-grade loop detection with:
- Stable tool key bucketing (read_file line ranges merged into 200-line buckets)
- Per-tool-type frequency limits (catches cross-file scanning loops)
- Per-tool frequency overrides (exempt high-frequency pipeline tools)
- Schema-safe warning injection (modifies AIMessage content, not inserting HumanMessage)
- Hard stop with full metadata cleanup (tool_calls, additional_kwargs, finish_reason)
- Defensive args normalization (handles str/None/non-dict args)

Detection strategy:
  1. After each model response, hash tool calls using stable keys (not raw args).
  2. Track recent hashes in a sliding window per thread.
  3. If same hash appears >= warn_threshold: inject warning INTO AIMessage content.
  4. If >= hard_limit: strip all tool_calls and force text output.
  5. Per-tool-type frequency counting catches "different args" loops.
"""

from __future__ import annotations

import hashlib
import json
import logging
import threading
from collections import OrderedDict, defaultdict
from copy import deepcopy
from typing import override

from langchain.agents import AgentState
from langchain.agents.middleware import AgentMiddleware
from langgraph.runtime import Runtime

logger = logging.getLogger(__name__)

# Defaults — can be overridden via constructor
_DEFAULT_WARN_THRESHOLD = 3
_DEFAULT_HARD_LIMIT = 5
_DEFAULT_WINDOW_SIZE = 20
_DEFAULT_MAX_TRACKED_THREADS = 100
_DEFAULT_TOOL_FREQ_WARN = 30
_DEFAULT_TOOL_FREQ_HARD_LIMIT = 50


# ---------------------------------------------------------------------------
# Stable key generation (noise-resistant hashing)
# ---------------------------------------------------------------------------


def _normalize_tool_call_args(raw_args: object) -> tuple[dict, str | None]:
    """Normalize tool call args to a dict plus an optional fallback key.

    Some providers serialize args as a JSON string instead of a dict.
    We defensively parse those cases so loop detection does not crash.
    """
    if isinstance(raw_args, dict):
        return raw_args, None

    if isinstance(raw_args, str):
        try:
            parsed = json.loads(raw_args)
        except (TypeError, ValueError, json.JSONDecodeError):
            return {}, raw_args

        if isinstance(parsed, dict):
            return parsed, None
        return {}, json.dumps(parsed, sort_keys=True, default=str)

    if raw_args is None:
        return {}, None

    return {}, json.dumps(raw_args, sort_keys=True, default=str)


def _stable_tool_key(name: str, args: dict, fallback_key: str | None) -> str:
    """Derive a stable key from salient args without overfitting to noise.

    Key design: for read_file, merge line ranges into 200-line buckets so
    reads of lines 1-100 and 2-101 produce the same key — preventing
    false negatives when the agent slightly adjusts read ranges.
    """
    if name == "read_file" and fallback_key is None:
        path = args.get("path") or ""
        start_line = args.get("start_line")
        end_line = args.get("end_line")

        bucket_size = 200
        try:
            start_line = int(start_line) if start_line is not None else 1
        except (TypeError, ValueError):
            start_line = 1
        try:
            end_line = int(end_line) if end_line is not None else start_line
        except (TypeError, ValueError):
            end_line = start_line

        start_line, end_line = sorted((start_line, end_line))
        bucket_start = (max(start_line, 1) - 1) // bucket_size
        bucket_end = (max(end_line, 1) - 1) // bucket_size
        return f"{path}:{bucket_start}-{bucket_end}"

    # write_file / str_replace are content-sensitive: hash full args
    if name in {"write_file", "str_replace"}:
        if fallback_key is not None:
            return fallback_key
        return json.dumps(args, sort_keys=True, default=str)

    # For other tools, use salient fields only
    salient_fields = ("path", "url", "query", "command", "pattern", "glob", "cmd")
    stable_args = {field: args[field] for field in salient_fields if args.get(field) is not None}
    if stable_args:
        return json.dumps(stable_args, sort_keys=True, default=str)

    if fallback_key is not None:
        return fallback_key

    return json.dumps(args, sort_keys=True, default=str)


def _hash_tool_calls(tool_calls: list[dict]) -> str:
    """Deterministic hash of a set of tool calls (name + stable key).

    Order-independent: same multiset of tool calls always produces same hash.
    """
    normalized: list[str] = []
    for tc in tool_calls:
        name = tc.get("name", "")
        args, fallback_key = _normalize_tool_call_args(tc.get("args", {}))
        key = _stable_tool_key(name, args, fallback_key)
        normalized.append(f"{name}:{key}")

    normalized.sort()
    blob = json.dumps(normalized, sort_keys=True, default=str)
    return hashlib.md5(blob.encode()).hexdigest()[:12]


# ---------------------------------------------------------------------------
# Messages
# ---------------------------------------------------------------------------

_WARNING_MSG = (
    "[LOOP DETECTED] You are repeating the same tool calls. "
    "Stop calling tools and produce your final answer now. "
    "If you cannot complete the task, summarize what you accomplished so far."
)

_TOOL_FREQ_WARNING_MSG = (
    "[LOOP DETECTED] You have called {tool_name} {count} times without producing "
    "a final answer. Stop calling tools and produce your final answer now. "
    "If you cannot complete the task, summarize what you accomplished so far."
)

_HARD_STOP_MSG = (
    "[FORCED STOP] Repeated tool calls exceeded the safety limit. "
    "Producing final answer with results collected so far."
)

_TOOL_FREQ_HARD_STOP_MSG = (
    "[FORCED STOP] Tool {tool_name} called {count} times — exceeded the "
    "per-tool safety limit. Producing final answer with results collected so far."
)


# ---------------------------------------------------------------------------
# Middleware
# ---------------------------------------------------------------------------


class LoopDetectionMiddleware(AgentMiddleware[AgentState]):
    """Detects and breaks repetitive tool call loops.

    Two detection layers:
    1. Hash-based: catches identical tool call sets (with stable key bucketing).
    2. Frequency-based: catches same tool type called many times with varying
       arguments (e.g. read_file on 40 different files).

    Schema-safe intervention:
    - Warning: appends text to AIMessage content (preserves tool_call pairing).
    - Hard stop: strips tool_calls + clears additional_kwargs + sets
      finish_reason="stop" (tricks API into accepting as normal response).

    Args:
        warn_threshold: Identical call sets before warning. Default: 3.
        hard_limit: Identical call sets before force-stop. Default: 5.
        window_size: Sliding window for tracking calls. Default: 20.
        max_tracked_threads: LRU eviction limit. Default: 100.
        tool_freq_warn: Per-tool-type calls before frequency warning. Default: 30.
        tool_freq_hard_limit: Per-tool-type calls before force-stop. Default: 50.
        tool_freq_overrides: Per-tool (warn, hard_limit) overrides. E.g.
            {"bash": (80, 100)} to exempt high-frequency pipeline tools.
    """

    def __init__(
        self,
        warn_threshold: int = _DEFAULT_WARN_THRESHOLD,
        hard_limit: int = _DEFAULT_HARD_LIMIT,
        window_size: int = _DEFAULT_WINDOW_SIZE,
        max_tracked_threads: int = _DEFAULT_MAX_TRACKED_THREADS,
        tool_freq_warn: int = _DEFAULT_TOOL_FREQ_WARN,
        tool_freq_hard_limit: int = _DEFAULT_TOOL_FREQ_HARD_LIMIT,
        tool_freq_overrides: dict[str, tuple[int, int]] | None = None,
    ):
        super().__init__()
        self.warn_threshold = warn_threshold
        self.hard_limit = hard_limit
        self.window_size = window_size
        self.max_tracked_threads = max_tracked_threads
        self.tool_freq_warn = tool_freq_warn
        self.tool_freq_hard_limit = tool_freq_hard_limit
        self._tool_freq_overrides: dict[str, tuple[int, int]] = tool_freq_overrides or {}
        self._lock = threading.Lock()
        self._history: OrderedDict[str, list[str]] = OrderedDict()
        self._warned: dict[str, set[str]] = defaultdict(set)
        self._tool_freq: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
        self._tool_freq_warned: dict[str, set[str]] = defaultdict(set)

    def _get_thread_id(self, runtime: Runtime) -> str:
        thread_id = runtime.context.get("thread_id") if runtime.context else None
        if thread_id:
            return thread_id
        return "default"

    def _evict_if_needed(self) -> None:
        """Evict LRU threads. Caller MUST hold self._lock."""
        while len(self._history) > self.max_tracked_threads:
            evicted_id, _ = self._history.popitem(last=False)
            self._warned.pop(evicted_id, None)
            self._tool_freq.pop(evicted_id, None)
            self._tool_freq_warned.pop(evicted_id, None)
            logger.debug("Evicted loop tracking for thread %s (LRU)", evicted_id)

    def _track_and_check(self, state: AgentState, runtime: Runtime) -> tuple[str | None, bool]:
        """Track tool calls and check for loops.

        Returns:
            (warning_message_or_none, should_hard_stop)
        """
        messages = state.get("messages", [])
        if not messages:
            return None, False

        last_msg = messages[-1]
        if getattr(last_msg, "type", None) != "ai":
            return None, False

        tool_calls = getattr(last_msg, "tool_calls", None)
        if not tool_calls:
            return None, False

        thread_id = self._get_thread_id(runtime)
        call_hash = _hash_tool_calls(tool_calls)

        with self._lock:
            if thread_id in self._history:
                self._history.move_to_end(thread_id)
            else:
                self._history[thread_id] = []
                self._evict_if_needed()

            history = self._history[thread_id]
            history.append(call_hash)
            if len(history) > self.window_size:
                history[:] = history[-self.window_size:]

            count = history.count(call_hash)
            tool_names = [tc.get("name", "?") for tc in tool_calls]

            # --- Layer 1: hash-based (identical call sets) ---
            if count >= self.hard_limit:
                logger.error(
                    "Loop hard limit reached — forcing stop",
                    extra={
                        "thread_id": thread_id,
                        "call_hash": call_hash,
                        "count": count,
                        "tools": tool_names,
                    },
                )
                return _HARD_STOP_MSG, True

            if count >= self.warn_threshold:
                warned = self._warned[thread_id]
                if call_hash not in warned:
                    warned.add(call_hash)
                    logger.warning(
                        "Repetitive tool calls detected — injecting warning",
                        extra={
                            "thread_id": thread_id,
                            "call_hash": call_hash,
                            "count": count,
                            "tools": tool_names,
                        },
                    )
                    return _WARNING_MSG, False

            # --- Layer 2: per-tool-type frequency ---
            freq = self._tool_freq[thread_id]
            for tc in tool_calls:
                name = tc.get("name", "")
                if not name:
                    continue
                freq[name] += 1
                tc_count = freq[name]

                if name in self._tool_freq_overrides:
                    eff_warn, eff_hard = self._tool_freq_overrides[name]
                else:
                    eff_warn, eff_hard = self.tool_freq_warn, self.tool_freq_hard_limit

                if tc_count >= eff_hard:
                    logger.error(
                        "Tool frequency hard limit reached — forcing stop",
                        extra={
                            "thread_id": thread_id,
                            "tool_name": name,
                            "count": tc_count,
                        },
                    )
                    return _TOOL_FREQ_HARD_STOP_MSG.format(tool_name=name, count=tc_count), True

                if tc_count >= eff_warn:
                    warned = self._tool_freq_warned[thread_id]
                    if name not in warned:
                        warned.add(name)
                        logger.warning(
                            "Tool frequency warning — too many calls to same tool type",
                            extra={
                                "thread_id": thread_id,
                                "tool_name": name,
                                "count": tc_count,
                            },
                        )
                        return _TOOL_FREQ_WARNING_MSG.format(tool_name=name, count=tc_count), False

        return None, False

    # ------------------------------------------------------------------
    # Schema-safe message mutation
    # ------------------------------------------------------------------

    @staticmethod
    def _append_text(content: str | list | None, text: str) -> str | list:
        """Append text to AIMessage content (handles str, list, and None).

        When content is a list of content blocks (e.g. Anthropic thinking mode),
        appends a new text block instead of concatenating to a list.
        """
        if content is None:
            return text
        if isinstance(content, list):
            return [*content, {"type": "text", "text": f"\n\n{text}"}]
        if isinstance(content, str):
            return content + f"\n\n{text}"
        return str(content) + f"\n\n{text}"

    @staticmethod
    def _build_hard_stop_update(last_msg, content: str | list) -> dict:
        """Clear tool-call metadata so forced-stop messages serialize as plain assistant text.

        This is critical for Schema compliance:
        - Removes tool_calls from the message
        - Clears tool_calls/function_call from additional_kwargs
        - Changes finish_reason from "tool_calls" to "stop"
        Without this, the API gateway would reject the response.
        """
        update: dict = {
            "tool_calls": [],
            "content": content,
        }

        # Clean additional_kwargs (remove tool_calls and function_call references)
        additional_kwargs = dict(getattr(last_msg, "additional_kwargs", {}) or {})
        for key in ("tool_calls", "function_call"):
            additional_kwargs.pop(key, None)
        update["additional_kwargs"] = additional_kwargs

        # Fix response_metadata finish_reason
        response_metadata = deepcopy(getattr(last_msg, "response_metadata", {}) or {})
        if response_metadata.get("finish_reason") == "tool_calls":
            response_metadata["finish_reason"] = "stop"
        update["response_metadata"] = response_metadata

        return update

    # ------------------------------------------------------------------
    # Core apply logic
    # ------------------------------------------------------------------

    def _apply(self, state: AgentState, runtime: Runtime) -> dict | None:
        warning, hard_stop = self._track_and_check(state, runtime)

        if hard_stop:
            # Strip tool_calls and force text output (Schema-safe)
            messages = state.get("messages", [])
            last_msg = messages[-1]
            content = self._append_text(last_msg.content, warning or _HARD_STOP_MSG)
            stripped_msg = last_msg.model_copy(
                update=self._build_hard_stop_update(last_msg, content)
            )
            return {"messages": [stripped_msg]}

        if warning:
            # Schema-safe warning: modify AIMessage content, NOT insert HumanMessage.
            # This preserves the AIMessage(tool_calls) ↔ ToolMessage pairing that
            # OpenAI/Claude strict validation requires.
            messages = state.get("messages", [])
            last_msg = messages[-1]
            patched_msg = last_msg.model_copy(
                update={"content": self._append_text(last_msg.content, warning)}
            )
            return {"messages": [patched_msg]}

        return None

    @override
    def after_model(self, state: AgentState, runtime: Runtime) -> dict | None:
        return self._apply(state, runtime)

    @override
    async def aafter_model(self, state: AgentState, runtime: Runtime) -> dict | None:
        return self._apply(state, runtime)

    def reset(self, thread_id: str | None = None) -> None:
        """Clear tracking state. If thread_id given, clear only that thread."""
        with self._lock:
            if thread_id:
                self._history.pop(thread_id, None)
                self._warned.pop(thread_id, None)
                self._tool_freq.pop(thread_id, None)
                self._tool_freq_warned.pop(thread_id, None)
            else:
                self._history.clear()
                self._warned.clear()
                self._tool_freq.clear()
                self._tool_freq_warned.clear()
