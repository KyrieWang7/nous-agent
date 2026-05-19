"""AIO (All-in-One) sandbox client - connects to external sandbox service via HTTP."""

import httpx

from src.sandbox import Sandbox


class AioSandbox(Sandbox):
    """Sandbox that connects to an external AIO sandbox service via HTTP."""

    def __init__(self, id: str, base_url: str):
        super().__init__(id)
        self._base_url = base_url.rstrip("/")
        self._client = httpx.Client(base_url=self._base_url, timeout=600)

    def execute_command(self, command: str) -> str:
        resp = self._client.post("/api/execute", json={"command": command})
        resp.raise_for_status()
        data = resp.json()
        return data.get("output", "")

    def read_file(self, path: str) -> str:
        resp = self._client.post("/api/read_file", json={"path": path})
        resp.raise_for_status()
        data = resp.json()
        return data.get("content", "")

    def list_dir(self, path: str, max_depth: int = 2) -> list[str]:
        resp = self._client.post("/api/list_dir", json={"path": path, "max_depth": max_depth})
        resp.raise_for_status()
        data = resp.json()
        return data.get("entries", [])

    def write_file(self, path: str, content: str, append: bool = False) -> None:
        resp = self._client.post("/api/write_file", json={
            "path": path,
            "content": content,
            "append": append,
        })
        resp.raise_for_status()

    def update_file(self, path: str, content: bytes) -> None:
        resp = self._client.post("/api/update_file", files={"file": content}, data={"path": path})
        resp.raise_for_status()

    def close(self):
        """Close HTTP client."""
        self._client.close()
