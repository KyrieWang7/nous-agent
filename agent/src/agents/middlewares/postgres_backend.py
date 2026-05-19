"""PostgresBackend for deepagents SummarizationMiddleware."""

import os
import logging

from deepagents.backends.protocol import (
    BackendProtocol, 
    WriteResult, 
    EditResult, 
    FileDownloadResponse, 
    FileUploadResponse, 
    FileInfo, 
    GrepMatch
)

from src.storage.history import write_conversation_history, read_conversation_history

logger = logging.getLogger(__name__)

class PostgresBackend(BackendProtocol):
    """DeepAgents BackendProtocol implementation that stores history in Postgres."""
    
    def _extract_thread_id(self, path: str) -> str | None:
        """Extract thread_id from a path like /conversation_history/<thread_id>.md"""
        basename = os.path.basename(path)
        if basename.endswith(".md"):
            return basename[:-3]
        return None

    async def awrite(self, file_path: str, content: str) -> WriteResult:
        thread_id = self._extract_thread_id(file_path)
        if not thread_id:
            return WriteResult(error=f"Invalid thread_id in path: {file_path}")
        
        try:
            await write_conversation_history(thread_id, content)
            return WriteResult(path=file_path, files_update=None)
        except Exception as e:
            logger.error("Error writing to PostgresBackend: %s", e, exc_info=True)
            return WriteResult(error=str(e))

    async def aedit(
        self,
        file_path: str,
        old_string: str,
        new_string: str,
        replace_all: bool = False,
    ) -> EditResult:
        thread_id = self._extract_thread_id(file_path)
        if not thread_id:
            return EditResult(error=f"Invalid thread_id in path: {file_path}")
        
        try:
            await write_conversation_history(thread_id, new_string)
            return EditResult(path=file_path, occurrences=1, files_update=None)
        except Exception as e:
            logger.error("Error editing in PostgresBackend: %s", e, exc_info=True)
            return EditResult(error=str(e))

    async def adownload_files(self, paths: list[str]) -> list[FileDownloadResponse]:
        results = []
        for path in paths:
            thread_id = self._extract_thread_id(path)
            if not thread_id:
                results.append(FileDownloadResponse(path=path, error="invalid_path"))
                continue
            
            try:
                content = await read_conversation_history(thread_id)
                if content is not None:
                    results.append(FileDownloadResponse(path=path, content=content.encode("utf-8")))
                else:
                    results.append(FileDownloadResponse(path=path, error="file_not_found"))
            except Exception as e:
                logger.error("Error reading from PostgresBackend: %s", e, exc_info=True)
                results.append(FileDownloadResponse(path=path, error="file_not_found"))
        return results
