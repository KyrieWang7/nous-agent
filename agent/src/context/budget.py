"""Token budget management for conversations.

Provides a lightweight tracker that estimates accumulated token usage and
signals when compaction should be triggered.

Supports two modes:
  - Estimate mode: 4 chars ≈ 1 token (default, no external dependency)
  - Precise mode: uses actual token counts reported by LLM API responses
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

logger = logging.getLogger(__name__)

_CHARS_PER_TOKEN = 4


@dataclass
class TokenBudget:
    """Track estimated token usage against a configurable ceiling.

    When ``update_from_api_response`` is called with actual usage data,
    the budget switches to precise tracking and no longer relies on the
    character-based heuristic.
    """

    max_tokens: int = 100_000
    _current_estimate: int = 0
    _precise: bool = False
    _history: list[tuple[str, int]] = field(default_factory=list, repr=False)

    @property
    def current(self) -> int:
        return self._current_estimate

    @property
    def remaining(self) -> int:
        return max(0, self.max_tokens - self._current_estimate)

    @property
    def utilisation(self) -> float:
        if self.max_tokens == 0:
            return 1.0
        return self._current_estimate / self.max_tokens

    @property
    def is_precise(self) -> bool:
        """True when budget is tracking actual API-reported token counts."""
        return self._precise

    def should_compact(self, threshold: float = 0.8) -> bool:
        """Return True when usage exceeds *threshold* fraction of the budget."""
        return self.utilisation >= threshold

    def add_text(self, text: str) -> None:
        tokens = len(text) // _CHARS_PER_TOKEN + 1
        self._current_estimate += tokens
        self._history.append(("estimate", tokens))

    def update_from_api_response(
        self,
        prompt_tokens: int | None = None,
        completion_tokens: int | None = None,
        total_tokens: int | None = None,
        cache_read_tokens: int | None = None,
        cache_creation_tokens: int | None = None,
    ) -> None:
        """Update budget from actual LLM API usage metadata.

        This switches the budget to precise mode on first call.
        """
        if total_tokens is not None:
            self._current_estimate = total_tokens
            self._precise = True
        elif prompt_tokens is not None:
            self._current_estimate = prompt_tokens + (completion_tokens or 0)
            self._precise = True

        if cache_read_tokens is not None:
            self._history.append(("cache_read", cache_read_tokens))
        if cache_creation_tokens is not None:
            self._history.append(("cache_creation", cache_creation_tokens))

    def set_estimate(self, tokens: int) -> None:
        self._current_estimate = tokens

    def reset(self) -> None:
        self._current_estimate = 0
        self._precise = False
        self._history.clear()
