from datetime import UTC, datetime
from uuid import UUID

from fastapi.testclient import TestClient
from src.storage import session_manager
from src.storage.session_manager import DEFAULT_LOCAL_USER_ID, SessionManager

from nous_gateway.app import create_app


def test_gateway_threads_list_returns_frontend_shape(monkeypatch):
    thread_id = UUID("11111111-1111-1111-1111-111111111111")
    created_at = datetime(2026, 5, 18, 10, 0, tzinfo=UTC)
    updated_at = datetime(2026, 5, 18, 10, 5, tzinfo=UTC)

    async def fake_list_sessions(
        user_id: str,
        limit: int = 50,
        offset: int = 0,
        include_archived: bool = False,
    ):
        assert user_id == DEFAULT_LOCAL_USER_ID
        assert limit == 2
        assert offset == 0
        assert include_archived is False
        return (
            [
                {
                    "id": thread_id,
                    "title": "分析蔚来股票",
                    "model_name": "gpt-4.1",
                    "created_at": created_at,
                    "updated_at": updated_at,
                    "is_archived": False,
                    "message_count": 3,
                }
            ],
            1,
        )

    monkeypatch.setattr(SessionManager, "list_sessions", fake_list_sessions)

    response = TestClient(create_app()).get("/api/threads?limit=2")

    assert response.status_code == 200
    data = response.json()
    assert data["total"] == 1
    assert data["threads"][0] == {
        "id": str(thread_id),
        "title": "分析蔚来股票",
        "model_name": "gpt-4.1",
        "created_at": "2026-05-18T10:00:00Z",
        "updated_at": "2026-05-18T10:05:00Z",
        "is_archived": False,
        "message_count": 3,
    }


def test_gateway_threads_create_uses_local_user_and_returns_thread(monkeypatch):
    thread_id = "22222222-2222-2222-2222-222222222222"
    created_at = datetime(2026, 5, 18, 10, 0, tzinfo=UTC)
    updated_at = datetime(2026, 5, 18, 10, 0, tzinfo=UTC)

    async def fake_create_session(
        thread_id_arg: str,
        user_id: str,
        title: str | None = None,
        model_name: str | None = None,
    ):
        assert thread_id_arg == thread_id
        assert user_id == DEFAULT_LOCAL_USER_ID
        assert title == "新任务"
        assert model_name == "gpt-4.1"
        return {
            "id": UUID(thread_id),
            "title": title,
            "model_name": model_name,
            "created_at": created_at,
            "updated_at": updated_at,
            "is_archived": False,
            "message_count": 0,
        }

    monkeypatch.setattr(SessionManager, "create_session", fake_create_session)

    response = TestClient(create_app()).post(
        "/api/threads",
        json={"id": thread_id, "title": "新任务", "model_name": "gpt-4.1"},
    )

    assert response.status_code == 200
    assert response.json()["id"] == thread_id
    assert response.json()["title"] == "新任务"
    assert response.json()["model_name"] == "gpt-4.1"
    assert response.json()["message_count"] == 0


def test_gateway_threads_list_returns_empty_when_storage_unavailable(monkeypatch):
    async def fake_list_sessions(*args, **kwargs):
        raise RuntimeError("database is starting")

    monkeypatch.setattr(SessionManager, "list_sessions", fake_list_sessions)

    response = TestClient(create_app()).get("/api/threads")

    assert response.status_code == 200
    assert response.json() == {"threads": [], "total": 0}


def test_threads_table_setup_seeds_default_local_user(monkeypatch):
    class FakeConnection:
        def __init__(self):
            self.sql = ""

        async def execute(self, sql: str):
            self.sql += sql

    class FakeConnectionContext:
        def __init__(self, conn: FakeConnection):
            self.conn = conn

        async def __aenter__(self):
            return self.conn

        async def __aexit__(self, exc_type, exc, tb):
            return False

    fake_conn = FakeConnection()

    monkeypatch.setattr(session_manager, "_threads_table_ready", False)
    monkeypatch.setattr(
        session_manager,
        "get_db_connection",
        lambda: FakeConnectionContext(fake_conn),
    )

    import asyncio

    asyncio.run(session_manager.setup_threads_table())

    assert "CREATE TABLE IF NOT EXISTS users" in fake_conn.sql
    assert "INSERT INTO users" in fake_conn.sql
    assert DEFAULT_LOCAL_USER_ID in fake_conn.sql
