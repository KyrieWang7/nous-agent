"""Tests for declarative @Next/@Prev middleware insertion."""

from __future__ import annotations

import pytest
from langchain.agents.middleware import AgentMiddleware

from src.agents.features import Next, Prev, RuntimeFeatures
from src.agents.middleware_ordering import insert_extra_middlewares
from src.agents.middlewares.clarification_middleware import ClarificationMiddleware


class _Base(AgentMiddleware):
    pass


class AnchorA(_Base):
    pass


class AnchorB(_Base):
    pass


def _chain():
    # Minimal base chain ending in ClarificationMiddleware (the terminal anchor).
    return [AnchorA(), AnchorB(), ClarificationMiddleware()]


def test_next_places_after_anchor():
    @Next(AnchorA)
    class AfterA(_Base):
        pass

    chain = _chain()
    insert_extra_middlewares(chain, [AfterA()])
    names = [type(m).__name__ for m in chain]
    assert names == ["AnchorA", "AfterA", "AnchorB", "ClarificationMiddleware"]


def test_prev_places_before_anchor():
    @Prev(AnchorB)
    class BeforeB(_Base):
        pass

    chain = _chain()
    insert_extra_middlewares(chain, [BeforeB()])
    names = [type(m).__name__ for m in chain]
    assert names == ["AnchorA", "BeforeB", "AnchorB", "ClarificationMiddleware"]


def test_unanchored_inserted_before_clarification():
    class Floating(_Base):
        pass

    chain = _chain()
    insert_extra_middlewares(chain, [Floating()])
    names = [type(m).__name__ for m in chain]
    assert names.index("Floating") == names.index("ClarificationMiddleware") - 1


def test_both_anchors_is_error():
    @Next(AnchorA)
    @Prev(AnchorB)
    class Confused(_Base):
        pass

    with pytest.raises(ValueError, match="cannot declare both"):
        insert_extra_middlewares(_chain(), [Confused()])


def test_conflicting_same_anchor_is_error():
    @Next(AnchorA)
    class FirstAfterA(_Base):
        pass

    @Next(AnchorA)
    class SecondAfterA(_Base):
        pass

    with pytest.raises(ValueError, match="both @Next"):
        insert_extra_middlewares(_chain(), [FirstAfterA(), SecondAfterA()])


def test_unresolvable_anchor_is_error():
    class Missing(_Base):
        pass

    @Next(Missing)
    class NeedsMissing(_Base):
        pass

    with pytest.raises(ValueError, match="not found in chain"):
        insert_extra_middlewares(_chain(), [NeedsMissing()])


def test_extra_to_extra_anchoring():
    @Next(AnchorA)
    class First(_Base):
        pass

    @Next(First)
    class Second(_Base):
        pass

    chain = _chain()
    # Provide them out of dependency order to exercise iterative resolution.
    insert_extra_middlewares(chain, [Second(), First()])
    names = [type(m).__name__ for m in chain]
    assert names == ["AnchorA", "First", "Second", "AnchorB", "ClarificationMiddleware"]


def test_empty_extras_is_noop():
    chain = _chain()
    insert_extra_middlewares(chain, [])
    assert [type(m).__name__ for m in chain] == ["AnchorA", "AnchorB", "ClarificationMiddleware"]


def test_runtime_features_defaults():
    f = RuntimeFeatures()
    assert f.sandbox is True
    assert f.tool_output_budget is True
    assert f.safety_finish_reason is True
