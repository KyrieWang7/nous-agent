"""Tests for the upgraded skills system ported from deer-flow.

Covers the capabilities the legacy line-based parser lacked:
- multiline YAML frontmatter (``>-`` folded scalars),
- ``allowed-tools`` parsing,
- slash-skill activation parsing/resolution,
- tool-policy filtering by skill allowlists,
- frontmatter validation.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from src.skills.parser import parse_allowed_tools, parse_skill_file
from src.skills.slash import parse_slash_skill_reference, resolve_slash_skill
from src.skills.tool_policy import (
    allowed_tool_names_for_skills,
    filter_tools_by_skill_allowed_tools,
)
from src.skills.types import Skill, SkillCategory
from src.skills.validation import _validate_skill_frontmatter


def _write_skill(tmp_path: Path, name: str, body: str) -> Path:
    skill_dir = tmp_path / name
    skill_dir.mkdir(parents=True, exist_ok=True)
    (skill_dir / "SKILL.md").write_text(body, encoding="utf-8")
    return skill_dir


def test_parser_handles_multiline_folded_description(tmp_path):
    body = (
        "---\n"
        "name: bootstrap\n"
        "description: >-\n"
        "  Generate a personalized SOUL.md through a warm conversation.\n"
        '  Trigger for: "create my SOUL.md", "bootstrap my agent".\n'
        "---\n\n"
        "# Bootstrap\n"
    )
    skill_dir = _write_skill(tmp_path, "bootstrap", body)
    skill = parse_skill_file(skill_dir / "SKILL.md", category="public")
    assert skill is not None
    assert skill.name == "bootstrap"
    # The folded scalar with embedded ':' must parse (the legacy parser could not).
    assert "create my SOUL.md" in skill.description
    assert skill.category == SkillCategory.PUBLIC


def test_parser_extracts_allowed_tools(tmp_path):
    body = (
        "---\n"
        "name: scoped\n"
        "description: A scoped skill\n"
        "allowed-tools:\n"
        "  - read_file\n"
        "  - bash\n"
        "---\n\n"
        "body\n"
    )
    skill_dir = _write_skill(tmp_path, "scoped", body)
    skill = parse_skill_file(skill_dir / "SKILL.md", category="public")
    assert skill is not None
    assert skill.allowed_tools == ["read_file", "bash"]


def test_parse_allowed_tools_rejects_non_list(tmp_path):
    with pytest.raises(ValueError):
        parse_allowed_tools("read_file", tmp_path / "SKILL.md")


def test_validation_rejects_bad_name(tmp_path):
    body = "---\nname: Bad_Name\ndescription: x\n---\n"
    skill_dir = _write_skill(tmp_path, "bad", body)
    is_valid, message, name = _validate_skill_frontmatter(skill_dir)
    assert not is_valid
    assert "hyphen-case" in message


def test_validation_accepts_good_skill(tmp_path):
    body = "---\nname: good-skill\ndescription: A fine skill\n---\n"
    skill_dir = _write_skill(tmp_path, "good", body)
    is_valid, _, name = _validate_skill_frontmatter(skill_dir)
    assert is_valid
    assert name == "good-skill"


def test_slash_reference_parsing():
    ref = parse_slash_skill_reference("/deep-research find papers on RAG")
    assert ref is not None
    assert ref.name == "deep-research"
    assert ref.remaining_text == "find papers on RAG"
    # Reserved control commands are not slash skills.
    assert parse_slash_skill_reference("/help") is None
    # Non-slash text returns None.
    assert parse_slash_skill_reference("just chatting") is None


def _make_skill(name: str, *, enabled: bool, allowed_tools=None) -> Skill:
    return Skill(
        name=name,
        description="d",
        license=None,
        skill_dir=Path("/tmp") / name,
        skill_file=Path("/tmp") / name / "SKILL.md",
        category=SkillCategory.PUBLIC,
        relative_path=Path(name),
        allowed_tools=allowed_tools,
        enabled=enabled,
    )


def test_slash_resolution_requires_enabled_skill():
    skills = [_make_skill("deep-research", enabled=True)]
    resolved = resolve_slash_skill("/deep-research do it", skills)
    assert resolved is not None
    assert resolved.skill.name == "deep-research"
    assert resolved.remaining_text == "do it"
    assert resolved.container_file_path.endswith("/public/deep-research/SKILL.md")

    disabled = [_make_skill("deep-research", enabled=False)]
    assert resolve_slash_skill("/deep-research do it", disabled) is None


class _FakeTool:
    def __init__(self, name: str) -> None:
        self.name = name


def test_tool_policy_allow_all_when_no_declaration():
    skills = [_make_skill("a", enabled=True, allowed_tools=None)]
    assert allowed_tool_names_for_skills(skills) is None
    tools = [_FakeTool("read_file"), _FakeTool("bash")]
    assert filter_tools_by_skill_allowed_tools(tools, skills) == tools


def test_tool_policy_union_of_declarations():
    skills = [
        _make_skill("a", enabled=True, allowed_tools=["read_file"]),
        _make_skill("b", enabled=True, allowed_tools=["bash"]),
        _make_skill("c", enabled=True, allowed_tools=None),  # legacy: contributes nothing
    ]
    allowed = allowed_tool_names_for_skills(skills)
    assert allowed == {"read_file", "bash"}

    tools = [_FakeTool("read_file"), _FakeTool("bash"), _FakeTool("web_search")]
    filtered = filter_tools_by_skill_allowed_tools(tools, skills)
    assert {t.name for t in filtered} == {"read_file", "bash"}


def test_get_available_tools_respects_skill_allowed_tools(monkeypatch):
    """When an enabled skill declares allowed-tools, the policy filter restricts the set."""
    from src.skills.tool_policy import filter_tools_by_skill_allowed_tools

    class _FakeTool:
        def __init__(self, name: str) -> None:
            self.name = name

    fake_tools = [_FakeTool("read_file"), _FakeTool("bash"), _FakeTool("web_search")]
    scoped = _make_skill("scoped", enabled=True, allowed_tools=["read_file", "bash"])

    filtered = filter_tools_by_skill_allowed_tools(fake_tools, [scoped])
    assert {t.name for t in filtered} == {"read_file", "bash"}
