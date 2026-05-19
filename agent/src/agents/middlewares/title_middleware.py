"""Middleware for automatic thread title generation.

After the first user→assistant exchange, generates a short title via LLM
and persists it both in the LangGraph checkpoint state *and* in the
gateway's ``threads`` table (via ``SessionManager``).
"""

import logging
from typing import NotRequired, override

from langchain.agents import AgentState
from langchain.agents.middleware import AgentMiddleware
from langgraph.runtime import Runtime

from src.config.title_config import get_title_config
from src.models import create_chat_model

logger = logging.getLogger(__name__)


class TitleMiddlewareState(AgentState):
    """Compatible with the ``ThreadState`` schema."""

    title: NotRequired[str | None]


class TitleMiddleware(AgentMiddleware[TitleMiddlewareState]):
    """Automatically generate a title for the thread after the first user message."""

    state_schema = TitleMiddlewareState

    @staticmethod
    def _content_to_text(content: object) -> str:
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            text_parts: list[str] = []
            for part in content:
                if isinstance(part, dict) and part.get("type") == "text":
                    text = part.get("text")
                    if isinstance(text, str):
                        text_parts.append(text)
                elif isinstance(part, str):
                    text_parts.append(part)
            return "\n".join(text_parts)
        return str(content) if content else ""

    def _should_generate_title(self, state: TitleMiddlewareState) -> bool:
        config = get_title_config()
        if not config.enabled:
            return False

        if state.get("title"):
            return False

        messages = state.get("messages", [])
        if len(messages) < 2:
            return False

        user_messages = [m for m in messages if m.type == "human"]
        assistant_messages = [m for m in messages if m.type == "ai"]

        return len(user_messages) == 1 and len(assistant_messages) >= 1

    def _generate_title(self, state: TitleMiddlewareState) -> str:
        config = get_title_config()
        messages = state.get("messages", [])

        user_msg_content = next((m.content for m in messages if m.type == "human"), "")
        assistant_msg_content = next((m.content for m in messages if m.type == "ai"), "")

        user_msg = self._content_to_text(user_msg_content)
        assistant_msg = self._content_to_text(assistant_msg_content)

        model = create_chat_model(name=config.model_name, thinking_enabled=False)

        prompt = config.prompt_template.format(
            max_words=config.max_words,
            user_msg=user_msg[:500],
            assistant_msg=assistant_msg[:500],
        )

        try:
            response = model.invoke(prompt)
            title_content = str(response.content) if response.content else ""
            title = title_content.strip().strip('"').strip("'")
            return title[: config.max_chars] if len(title) > config.max_chars else title
        except Exception as e:
            logger.warning("Failed to generate title: %s", e)
            fallback_chars = min(config.max_chars, 50)
            if len(user_msg) > fallback_chars:
                return user_msg[:fallback_chars].rstrip() + "..."
            return user_msg if user_msg else "New Conversation"

    @override
    def after_agent(self, state: TitleMiddlewareState, runtime: Runtime) -> dict | None:
        if self._should_generate_title(state):
            title = self._generate_title(state)
            logger.info("Generated thread title: %s", title)
            return {"title": title}
        return None

    @override
    async def aafter_agent(self, state: TitleMiddlewareState, runtime: Runtime) -> dict | None:
        """Async version: generate title and sync to the ``threads`` table."""
        result = self.after_agent(state, runtime)
        if result and "title" in result:
            thread_id = runtime.context.get("thread_id")
            if thread_id:
                try:
                    from src.storage.session_manager import SessionManager
                    await SessionManager.update_title(thread_id, result["title"])
                    logger.info("Synced title to threads table: thread_id=%s", thread_id)
                except Exception:
                    logger.warning("Failed to sync title to threads table", exc_info=True)
        return result
