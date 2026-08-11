import inspect
import json
from pathlib import Path

from src.tools.builtins.task_tool import task_tool


CONTRACT = json.loads(
    (Path(__file__).resolve().parents[2] / "contracts" / "harness_protocol_contract.json").read_text()
)


def test_python_task_signature_matches_shared_required_fields():
    parameters = inspect.signature(task_tool.func).parameters
    assert all(field in parameters for field in CONTRACT["task_required_fields"])


def test_shared_runtime_fields_are_supported_by_python_lead_agent():
    source = (Path(__file__).resolve().parents[1] / "src" / "agents" / "lead_agent" / "agent.py").read_text()
    assert all(field in source for field in CONTRACT["runtime_context_fields"])
