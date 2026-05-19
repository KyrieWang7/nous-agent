import logging
import mimetypes
from typing import Annotated

from langchain.tools import InjectedToolCallId, ToolRuntime, tool
from langchain_core.messages import ToolMessage
from langgraph.types import Command
from langgraph.typing import ContextT

from src.agents.thread_state import ThreadState
from src.sandbox.tools import ensure_sandbox_initialized

logger = logging.getLogger(__name__)

_TEXT_MIME_PREFIXES = ("text/",)
_TEXT_MIME_TYPES = {
    "application/json",
    "application/xml",
    "application/javascript",
    "application/x-yaml",
    "application/toml",
    "application/csv",
}


def _is_text_file(filepath: str) -> bool:
    mime_type, _ = mimetypes.guess_type(filepath)
    if not mime_type:
        return False
    if any(mime_type.startswith(p) for p in _TEXT_MIME_PREFIXES):
        return True
    return mime_type in _TEXT_MIME_TYPES


def _sync_file_to_minio(sandbox, thread_id: str, filepath: str) -> None:
    """Read a file from the sandbox and upload it to MinIO.

    Uses text read for text files, binary read (base64) for everything else.
    """
    from src.storage import upload_artifact

    mime_type, _ = mimetypes.guess_type(filepath)
    content_type = mime_type or "application/octet-stream"

    if _is_text_file(filepath):
        text = sandbox.read_file(filepath)
        if text and not text.startswith("Error:"):
            upload_artifact(
                thread_id=thread_id,
                virtual_path=filepath,
                content=text.encode("utf-8"),
                content_type=content_type,
            )
            return
        logger.warning(f"Could not read text file from sandbox: {filepath}")
        return

    data = sandbox.read_file_bytes(filepath)
    if data:
        upload_artifact(
            thread_id=thread_id,
            virtual_path=filepath,
            content=data,
            content_type=content_type,
        )
    else:
        logger.warning(f"Could not read binary file from sandbox: {filepath}")


@tool("present_files", parse_docstring=True)
def present_file_tool(
    runtime: ToolRuntime[ContextT, ThreadState],
    filepaths: list[str],
    tool_call_id: Annotated[str, InjectedToolCallId],
) -> Command:
    """Make files visible to the user for viewing and rendering in the client interface.

    When to use the present_files tool:

    - Making any file available for the user to view, download, or interact with
    - Presenting multiple related files at once
    - After creating files that should be presented to the user

    When NOT to use the present_files tool:
    - When you only need to read file contents for your own processing
    - For temporary or intermediate files not meant for user viewing

    Notes:
    - You should call this tool after creating files and moving them to the `/mnt/user-data/outputs` directory.
    - This tool can be safely called in parallel with other tools. State updates are handled by a reducer to prevent conflicts.

    Args:
        filepaths: List of absolute file paths to present to the user. **Only** files in `/mnt/user-data/outputs` can be presented.
    """
    thread_id = runtime.context.get("thread_id") if runtime else None

    if thread_id:
        try:
            sandbox = ensure_sandbox_initialized(runtime)
            for fp in filepaths:
                _sync_file_to_minio(sandbox, thread_id, fp)
        except Exception as e:
            logger.error(f"Failed to sync files to MinIO: {e}")

    return Command(
        update={"artifacts": filepaths, "messages": [ToolMessage("Successfully presented files", tool_call_id=tool_call_id)]},
    )
