"""Contract test for the structured subagent status field.

Loads the shared fixture at ``contracts/subagent_status_contract.json`` (the
single source of truth shared with the frontend) and asserts the backend parser
and stamper agree with every case. The frontend has a mirror test that loads the
same fixture, so both sides cannot drift.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from langchain_core.messages import ToolMessage

from src.agents.middlewares.tool_error_handling_middleware import (
    _stamp_task_subagent_status,
)
from src.subagents.status_contract import (
    SUBAGENT_ERROR_KEY,
    SUBAGENT_STATUS_KEY,
    SUBAGENT_STATUS_VALUES,
    extract_subagent_status,
    make_subagent_additional_kwargs,
)

_CONTRACT_PATH = Path(__file__).resolve().parents[2] / "contracts" / "subagent_status_contract.json"


def _load_fixture() -> dict:
    return json.loads(_CONTRACT_PATH.read_text(encoding="utf-8"))


def test_fixture_exists_and_values_align():
    fixture = _load_fixture()
    assert tuple(fixture["valid_status_values"]) == SUBAGENT_STATUS_VALUES


@pytest.mark.parametrize("case", _load_fixture()["cases"], ids=lambda c: c["name"])
def test_parser_matches_fixture(case: dict):
    assert extract_subagent_status(case["content"]) == case["expected_status"]


@pytest.mark.parametrize("case", _load_fixture()["cases"], ids=lambda c: c["name"])
def test_stamper_matches_fixture(case: dict):
    msg = ToolMessage(content=case["content"], tool_call_id="tc-1", name="task")
    stamped = _stamp_task_subagent_status(msg, tool_name="task")
    expected = case["expected_status"]
    if expected is None:
        # Non-terminal chunk: no stamp so the frontend keeps the in-progress card.
        assert SUBAGENT_STATUS_KEY not in stamped.additional_kwargs
    else:
        assert stamped.additional_kwargs.get(SUBAGENT_STATUS_KEY) == expected


def test_stamper_is_noop_for_non_task_tools():
    msg = ToolMessage(content="Task Succeeded. Result: ok", tool_call_id="tc-1", name="read_file")
    stamped = _stamp_task_subagent_status(msg, tool_name="read_file")
    assert stamped is msg
    assert SUBAGENT_STATUS_KEY not in (stamped.additional_kwargs or {})


def test_error_field_carried_when_present():
    kwargs = make_subagent_additional_kwargs("failed", error="RuntimeError boom")
    assert kwargs[SUBAGENT_STATUS_KEY] == "failed"
    assert kwargs[SUBAGENT_ERROR_KEY] == "RuntimeError boom"


def test_blank_error_field_dropped():
    kwargs = make_subagent_additional_kwargs("failed", error="   ")
    assert SUBAGENT_ERROR_KEY not in kwargs


def test_invalid_status_rejected():
    with pytest.raises(ValueError):
        make_subagent_additional_kwargs("bogus")  # type: ignore[arg-type]
