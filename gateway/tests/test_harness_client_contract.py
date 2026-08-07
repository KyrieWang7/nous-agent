from unittest.mock import MagicMock, patch

from src.client import DeerFlowClient

from nous_gateway.routers.models import ModelsListResponse
from nous_gateway.routers.uploads import UploadResponse


def test_python_harness_model_response_matches_gateway_contract():
    model = MagicMock()
    model.name = "test-model"
    model.display_name = "Test Model"
    model.description = "A test model"
    model.supports_thinking = False
    model.supports_reasoning_effort = False
    config = MagicMock(models=[model])

    with patch("src.client.get_app_config", return_value=config):
        response = DeerFlowClient().list_models()

    parsed = ModelsListResponse(**response)
    assert [item.name for item in parsed.models] == ["test-model"]


def test_python_harness_upload_response_matches_gateway_contract(tmp_path):
    source = tmp_path / "hello.txt"
    source.write_text("hello", encoding="utf-8")
    uploads = tmp_path / "uploads"
    uploads.mkdir()

    config = MagicMock(models=[])
    with (
        patch("src.client.get_app_config", return_value=config),
        patch.object(DeerFlowClient, "_get_uploads_dir", return_value=uploads),
    ):
        response = DeerFlowClient().upload_files("thread-1", [source])

    parsed = UploadResponse(**response)
    assert parsed.success is True
    assert parsed.files[0]["filename"] == "hello.txt"
