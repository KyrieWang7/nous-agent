from fastapi.testclient import TestClient

from nous_gateway.app import app


def test_models_are_loaded_from_selected_go_harness_config(tmp_path, monkeypatch):
    config = tmp_path / "config.yaml"
    config.write_text(
        """
models:
  - name: gateway-model
    display_name: Gateway Model
    supports_thinking: true
""",
        encoding="utf-8",
    )
    monkeypatch.setenv("NOUS_HARNESS_CONFIG_PATH", str(config))

    response = TestClient(app).get("/api/models")

    assert response.status_code == 200
    assert response.json() == {
        "models": [
            {
                "name": "gateway-model",
                "display_name": "Gateway Model",
                "description": None,
                "supports_thinking": True,
                "supports_reasoning_effort": False,
            }
        ]
    }
