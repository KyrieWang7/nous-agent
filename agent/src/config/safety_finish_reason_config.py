"""Configuration for provider safety-termination suppression.

Ported from deer-flow (deerflow.config.safety_finish_reason_config). Adapted to
the nous-agent ``src.*`` package layout.

When a provider safety-terminates a response (OpenAI ``finish_reason=content_filter``,
Anthropic ``stop_reason=refusal``, Gemini ``finish_reason=SAFETY`` ...) while still
emitting partially-formed ``tool_calls``, the middleware strips those tool calls so
truncated/unsafe arguments are never dispatched.
"""

from __future__ import annotations

from pydantic import BaseModel, Field


class SafetyDetectorEntry(BaseModel):
    """A single reflection-loaded detector entry.

    ``use`` is a ``module:attr`` path resolved to a ``SafetyTerminationDetector``.
    ``config`` is passed to the detector constructor as keyword arguments.
    """

    use: str = Field(description="module:attr path to a SafetyTerminationDetector class.")
    config: dict = Field(default_factory=dict, description="Keyword args for the detector constructor.")


class SafetyFinishReasonConfig(BaseModel):
    """Config section for the safety-termination suppression middleware."""

    enabled: bool = Field(
        default=True,
        description="Enable suppression of tool calls when the provider safety-terminated the response.",
    )
    detectors: list[SafetyDetectorEntry] | None = Field(
        default=None,
        description=(
            "Custom detector list. Omit (null) to use the built-in detectors "
            "(OpenAI content_filter, Anthropic refusal, Gemini SAFETY). An empty "
            "list is rejected — use enabled=false to disable the middleware entirely."
        ),
    )


def load_safety_finish_reason_config_from_dict(data: dict | None) -> SafetyFinishReasonConfig:
    """Build a SafetyFinishReasonConfig from a raw config mapping (or defaults when absent)."""
    if not data:
        return SafetyFinishReasonConfig()
    return SafetyFinishReasonConfig(**data)
