"""HookRunner — orchestrates hook execution for a given event."""

from __future__ import annotations

import logging
from typing import Any

from src.hooks.external import run_external_hook
from src.hooks.python_hook import resolve_python_hook, run_python_hook
from src.hooks.types import HookConfig, HookEvent, HookPayload, HookResult

logger = logging.getLogger(__name__)


class HookRunner:
    """Run a list of hooks sequentially for tool lifecycle events.

    Each hook can:
      - allow (continue to next hook / actual execution)
      - deny  (short-circuit — stop further hooks and block execution)
      - warn  (log but continue)
    """

    def __init__(self, hooks: list[HookConfig] | None = None):
        self._hooks: list[HookConfig] = hooks or []

    @classmethod
    def from_config(cls, raw: dict[str, Any] | None) -> HookRunner:
        """Build a runner from the ``hooks`` section of config.yaml."""
        if not raw:
            return cls()

        configs: list[HookConfig] = []
        for event_key in ("pre_tool_use", "post_tool_use", "post_tool_use_failure"):
            entries = raw.get(event_key, [])
            for entry in entries:
                if isinstance(entry, str):
                    entry = {"command": entry}
                cfg = HookConfig(
                    command=entry.get("command"),
                    use=entry.get("use"),
                    tools=entry.get("tools"),
                    events=[event_key],
                )
                configs.append(cfg)
        return cls(configs)

    def run(
        self,
        event: HookEvent,
        tool_name: str,
        tool_input: dict[str, Any] | None = None,
        tool_output: str | None = None,
        is_error: bool = False,
    ) -> HookResult:
        """Execute matching hooks in order; return the aggregated result."""
        matching = [h for h in self._hooks if h.matches_event(event) and h.matches_tool(tool_name)]
        if not matching:
            return HookResult.allowed()

        payload = HookPayload(
            event=event.value,
            tool_name=tool_name,
            tool_input=tool_input,
            tool_output=tool_output,
            is_error=is_error,
        )

        messages: list[str] = []
        updated_input: dict[str, Any] | None = None
        additional_contexts: list[str] = []

        for hook in matching:
            result = self._run_single(hook, payload)

            if result.message:
                messages.append(result.message)
            if result.updated_input is not None:
                updated_input = result.updated_input
                payload = HookPayload(
                    event=payload.event,
                    tool_name=payload.tool_name,
                    tool_input=updated_input,
                    tool_output=payload.tool_output,
                    is_error=payload.is_error,
                )
            additional_contexts.extend(result.additional_contexts)

            if result.is_denied():
                return HookResult(
                    outcome="deny",
                    message="\n".join(messages) if messages else result.message,
                    updated_input=updated_input,
                    additional_contexts=additional_contexts,
                )

        combined_message = "\n".join(messages) if messages else None
        return HookResult(
            outcome="allow",
            message=combined_message,
            updated_input=updated_input,
            additional_contexts=additional_contexts,
        )

    def _run_single(self, hook: HookConfig, payload: HookPayload) -> HookResult:
        if hook.command:
            return run_external_hook(hook.command, payload)
        if hook.use:
            try:
                func = resolve_python_hook(hook.use)
                return run_python_hook(func, payload)
            except Exception as exc:
                logger.exception("Failed to resolve Python hook '%s'", hook.use)
                return HookResult.warned(f"Failed to load hook {hook.use}: {exc}")
        logger.warning("Hook config has neither 'command' nor 'use'; skipping")
        return HookResult.allowed()
