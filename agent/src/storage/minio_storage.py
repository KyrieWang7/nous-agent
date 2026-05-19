"""MinIO object storage client for artifact persistence.

Artifacts written by agents inside the sandbox are uploaded here via
``present_files``, and served to the frontend by the gateway.

Bucket layout::

    nous-ai-minio/
      {thread_id}/outputs/index.html
      {thread_id}/outputs/style.css
      {thread_id}/workspace/main.py

Environment variables::

    MINIO_ENDPOINT   – host:port   (default: host.docker.internal:9090)
    MINIO_ACCESS_KEY – access key  (default: minio)
    MINIO_SECRET_KEY – secret key  (default: miniosecret)
    MINIO_BUCKET     – bucket name (default: nous-ai-minio)
    MINIO_SECURE     – use TLS     (default: false)
"""

from __future__ import annotations

import io
import logging
import os

from minio import Minio
from minio.error import S3Error

logger = logging.getLogger(__name__)

BUCKET_NAME = os.getenv("MINIO_BUCKET", "nous-ai-minio")

_client: Minio | None = None


def get_storage_client() -> Minio:
    global _client
    if _client is None:
        endpoint = os.getenv("MINIO_ENDPOINT", "host.docker.internal:9090")
        access_key = os.getenv("MINIO_ACCESS_KEY", "minio")
        secret_key = os.getenv("MINIO_SECRET_KEY", "miniosecret")
        secure = os.getenv("MINIO_SECURE", "false").lower() == "true"

        _client = Minio(endpoint, access_key=access_key, secret_key=secret_key, secure=secure)

        if not _client.bucket_exists(BUCKET_NAME):
            _client.make_bucket(BUCKET_NAME)
            logger.info(f"Created MinIO bucket: {BUCKET_NAME}")

    return _client


def _object_key(thread_id: str, virtual_path: str) -> str:
    """Convert a virtual sandbox path to a MinIO object key.

    /mnt/user-data/outputs/index.html → {thread_id}/outputs/index.html
    """
    prefix = "/mnt/user-data"
    stripped = virtual_path.lstrip("/")
    mnt_prefix = prefix.lstrip("/")

    if stripped.startswith(mnt_prefix):
        relative = stripped[len(mnt_prefix):].lstrip("/")
    else:
        relative = stripped

    return f"{thread_id}/{relative}"


def upload_artifact(thread_id: str, virtual_path: str, content: bytes, content_type: str = "application/octet-stream") -> str:
    """Upload artifact content to MinIO.

    Returns:
        The object key in MinIO.
    """
    client = get_storage_client()
    key = _object_key(thread_id, virtual_path)

    client.put_object(
        BUCKET_NAME,
        key,
        io.BytesIO(content),
        length=len(content),
        content_type=content_type,
    )
    logger.info(f"Uploaded artifact: {key} ({len(content)} bytes)")
    return key


def download_artifact(thread_id: str, virtual_path: str) -> tuple[bytes, str]:
    """Download artifact content from MinIO.

    Returns:
        Tuple of (content_bytes, content_type).

    Raises:
        S3Error: If the object does not exist.
    """
    client = get_storage_client()
    key = _object_key(thread_id, virtual_path)

    response = client.get_object(BUCKET_NAME, key)
    try:
        content = response.read()
        content_type = response.headers.get("Content-Type", "application/octet-stream")
        return content, content_type
    finally:
        response.close()
        response.release_conn()


def artifact_exists(thread_id: str, virtual_path: str) -> bool:
    """Check if an artifact exists in MinIO."""
    client = get_storage_client()
    key = _object_key(thread_id, virtual_path)
    try:
        client.stat_object(BUCKET_NAME, key)
        return True
    except S3Error:
        return False
