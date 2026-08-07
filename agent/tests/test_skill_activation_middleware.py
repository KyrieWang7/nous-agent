"""Tests for SkillActivationMiddleware (slash skill activation).

Exercises the runtime wiring of the ported slash activation middleware against a
small in-memory skills set, without depending on config.yaml or the real skills
directory.
"""

from __future__ import annotations

from pathlib import Path

import pytest
from langchain_core.messages import AIMessage, HumanMessage

from src.agents.middlewares.skill_activation_middleware import (
    SkillActivationMiddleware,
    is_slash_skill_activation_reminder,
)
from src.skills.types import Skill, SkillCategory
from src.utils.messages import ORIGINAL_USER_CONTENT_KEY


class _FakeReq:
    def __init__(self, messages):
        self.messages = messages
        self.runtime = None

    def override(self, messages=None):
        req = _FakeReq(messages if messages is not None else self.messages)
        return req


def _handler_capture(store):
    def handler(req):
        store["messages"] = list(req.messages)
        return AIMessage(content="ok")

    return handler


@pytest.fixture
def patched_skill(tmp_path, monkeypatch):
    """Create one enabled skill on disk and route the middleware to it."""
    skills_root = tmp_path / "skills"
    skill_dir = skills_root / "public" / "demo-skill"
    skill_dir.mkdir(parents=True)
    skill_file = skill_dir / "SKILL.md"
    skill_file.write_text(
        "---\nname: demo-skill\ndescription: A demo skill\n---\n\n# Demo\nDo the demo steps.\n",
        encoding="utf-8",
    )

    skill = Skill(
        name="demo-skill",
        description="A demo skill",
        license=None,
        skill_dir=skill_dir,
        skill_file=skill_file,
        category=SkillCategory.PUBLIC,
        relative_path=Path("demo-skill"),
        allowed_tools=None,
        enabled=True,
    )

    monkeypatch.setattr("src.skills.loader.load_skills", lambda *a, **k: [skill])
    monkeypatch.setattr(SkillActivationMiddleware, "_container_base_path", staticmethod(lambda: "/mnt/skills"))
    monkeypatch.setattr(SkillActivationMiddleware, "_skills_root", staticmethod(lambda: skills_root))
    return skill


def test_activates_installed_skill(patched_skill):
    mw = SkillActivationMiddleware()
    store: dict = {}
    req = _FakeReq([HumanMessage(content="/demo-skill run it", id="u1")])

    result = mw.wrap_model_call(req, _handler_capture(store))
    assert result.content == "ok"

    injected = [m for m in store["messages"] if is_slash_skill_activation_reminder(m)]
    assert len(injected) == 1
    msg = injected[0]
    assert msg.additional_kwargs.get("hide_from_ui") is True
    assert "demo-skill" in msg.content
    assert "Do the demo steps." in msg.content
    # The injected reminder is placed before the user message.
    assert store["messages"].index(msg) < store["messages"].index(
        next(m for m in store["messages"] if getattr(m, "id", None) == "u1")
    )


def test_unknown_skill_short_circuits(monkeypatch):
    monkeypatch.setattr("src.skills.loader.load_skills", lambda *a, **k: [])
    mw = SkillActivationMiddleware()
    req = _FakeReq([HumanMessage(content="/ghost do", id="u2")])
    result = mw.wrap_model_call(req, _handler_capture({}))
    assert isinstance(result, AIMessage)
    assert "not installed" in result.content


def test_non_slash_passthrough(patched_skill):
    mw = SkillActivationMiddleware()
    store: dict = {}
    req = _FakeReq([HumanMessage(content="hello there", id="u3")])
    result = mw.wrap_model_call(req, _handler_capture(store))
    assert result.content == "ok"
    assert not any(is_slash_skill_activation_reminder(m) for m in store["messages"])


def test_idempotent_when_already_activated(patched_skill):
    mw = SkillActivationMiddleware()
    target = HumanMessage(content="/demo-skill run it", id="u4")
    reminder = SkillActivationMiddleware._make_activation_message(target, "<slash_skill_activation>prev</slash_skill_activation>")
    store: dict = {}
    req = _FakeReq([reminder, target])
    mw.wrap_model_call(req, _handler_capture(store))
    injected = [m for m in store["messages"] if is_slash_skill_activation_reminder(m)]
    # No new activation injected — the existing one is detected.
    assert len(injected) == 1


def test_reads_original_content_after_uploads_rewrite(patched_skill):
    """Slash command survives the uploads middleware rewriting the user message."""
    mw = SkillActivationMiddleware()
    store: dict = {}
    rewritten = HumanMessage(
        content="<uploaded_files>...</uploaded_files>\n\n/demo-skill run it",
        id="u5",
        additional_kwargs={ORIGINAL_USER_CONTENT_KEY: "/demo-skill run it"},
    )
    req = _FakeReq([rewritten])
    mw.wrap_model_call(req, _handler_capture(store))
    injected = [m for m in store["messages"] if is_slash_skill_activation_reminder(m)]
    assert len(injected) == 1
