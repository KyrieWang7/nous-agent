from src.agents.memory.updater import _parse_update_data_from_response


def test_parse_update_data_handles_plain_json():
    payload = '{"user": {}, "history": {}, "newFacts": [], "factsToRemove": []}'
    parsed = _parse_update_data_from_response(payload)
    assert parsed is not None
    assert parsed["newFacts"] == []


def test_parse_update_data_handles_fenced_json():
    payload = """```json
{"user": {}, "history": {}, "newFacts": [], "factsToRemove": []}
```"""
    parsed = _parse_update_data_from_response(payload)
    assert parsed is not None
    assert parsed["factsToRemove"] == []


def test_parse_update_data_handles_wrapped_text():
    payload = """
这是你的结果：
{"user": {}, "history": {}, "newFacts": [], "factsToRemove": []}
请查收。
"""
    parsed = _parse_update_data_from_response(payload)
    assert parsed is not None
    assert parsed["user"] == {}


def test_parse_update_data_handles_content_blocks():
    payload = [
        {"type": "reasoning", "text": "先分析"},
        {"type": "text", "text": '{"user": {}, "history": {}, "newFacts": [], "factsToRemove": []}'},
    ]
    parsed = _parse_update_data_from_response(payload)
    assert parsed is not None
    assert parsed["history"] == {}
