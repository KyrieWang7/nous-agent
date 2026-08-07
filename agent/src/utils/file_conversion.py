"""File conversion helpers used by the Python harness client.

This module deliberately lives outside the Gateway package so the harness can
be installed and used without the product control-plane service.
"""

import logging
from pathlib import Path

logger = logging.getLogger(__name__)

CONVERTIBLE_EXTENSIONS = {
    ".pdf",
    ".ppt",
    ".pptx",
    ".xls",
    ".xlsx",
    ".doc",
    ".docx",
}


async def convert_file_to_markdown(file_path: Path) -> Path | None:
    """Convert a supported document to a sibling Markdown file."""
    try:
        from markitdown import MarkItDown

        result = MarkItDown().convert(str(file_path))
        markdown_path = file_path.with_suffix(".md")
        markdown_path.write_text(result.text_content, encoding="utf-8")
        return markdown_path
    except Exception as exc:
        logger.error("Failed to convert %s to markdown: %s", file_path.name, exc)
        return None
