"""Patched ChatDeepSeek that preserves reasoning_content in multi-turn conversations.

This module provides a patched version of ChatDeepSeek that properly handles
reasoning_content when sending messages back to the API. The original implementation
(langchain_openai) does NOT capture reasoning_content from streaming deltas, so it
never gets stored in AIMessage.additional_kwargs. This means subsequent API calls
are missing reasoning_content, which DeepSeek's thinking-mode API requires.

This patch fixes BOTH sides:
1. RECEIVING: Override _convert_chunk_to_generation_chunk to capture reasoning_content
   from streaming deltas and store it in additional_kwargs.
2. SENDING: Override _get_request_payload to inject reasoning_content from
   additional_kwargs back into the outgoing API payload.
"""

import logging
from collections.abc import Mapping
from typing import Any, cast

from langchain_core.language_models import LanguageModelInput
from langchain_core.messages import AIMessage, AIMessageChunk
from langchain_core.outputs import ChatGenerationChunk
from langchain_deepseek import ChatDeepSeek
from langchain_openai import ChatOpenAI

logger = logging.getLogger(__name__)


def _inject_reasoning_content(payload: dict, original_messages: list) -> dict:
    """Add DeepSeek reasoning_content back to assistant payload messages.

    Iterates over assistant messages in the payload and injects reasoning_content
    from the corresponding AIMessage's additional_kwargs.
    """
    payload_messages = payload.get("messages", [])
    if not payload_messages:
        return payload

    # Collect all AIMessages (preserving order)
    ai_messages_with_reasoning = []
    for msg in original_messages:
        if isinstance(msg, AIMessage):
            reasoning = msg.additional_kwargs.get("reasoning_content")
            ai_messages_with_reasoning.append((msg, reasoning))

    if not ai_messages_with_reasoning:
        return payload

    # Fast path: lengths match, do direct 1:1 mapping
    if len(payload_messages) == len(original_messages):
        for payload_msg, orig_msg in zip(payload_messages, original_messages):
            if payload_msg.get("role") == "assistant" and isinstance(orig_msg, AIMessage):
                reasoning_content = orig_msg.additional_kwargs.get("reasoning_content")
                if reasoning_content is not None:
                    payload_msg["reasoning_content"] = reasoning_content
        return payload

    # Fallback: match by order of assistant messages
    assistant_indices = [
        i for i, m in enumerate(payload_messages) if m.get("role") == "assistant"
    ]

    for idx, (ai_msg, reasoning) in zip(assistant_indices, ai_messages_with_reasoning):
        if reasoning is not None:
            payload_messages[idx]["reasoning_content"] = reasoning

    if len(assistant_indices) != len(ai_messages_with_reasoning):
        logger.debug(
            "reasoning_content injection: %d assistant payloads vs %d AI messages",
            len(assistant_indices),
            len(ai_messages_with_reasoning),
        )

    return payload


class PatchedChatDeepSeek(ChatDeepSeek):
    """ChatDeepSeek with proper reasoning_content preservation.

    When using thinking/reasoning enabled models, the API expects reasoning_content
    to be present on ALL assistant messages in multi-turn conversations. This patched
    version ensures reasoning_content from additional_kwargs is included in the
    request payload.
    """

    def _get_request_payload(
        self,
        input_: LanguageModelInput,
        *,
        stop: list[str] | None = None,
        **kwargs: Any,
    ) -> dict:
        """Get request payload with reasoning_content preserved."""
        original_messages = self._convert_input(input_).to_messages()
        payload = super()._get_request_payload(input_, stop=stop, **kwargs)
        return _inject_reasoning_content(payload, original_messages)


class PatchedChatDeepSeekOpenAI(ChatOpenAI):
    """OpenAI-compatible DeepSeek client with reasoning_content preservation.

    Fixes two issues:
    1. Captures reasoning_content from streaming deltas into additional_kwargs
       so it gets persisted in LangGraph state.
    2. Injects reasoning_content back into API payloads on subsequent calls.
    """

    def _get_request_payload(
        self,
        input_: LanguageModelInput,
        *,
        stop: list[str] | None = None,
        **kwargs: Any,
    ) -> dict:
        """Get request payload with reasoning_content preserved."""
        original_messages = self._convert_input(input_).to_messages()
        payload = super()._get_request_payload(input_, stop=stop, **kwargs)
        return _inject_reasoning_content(payload, original_messages)

    def _convert_chunk_to_generation_chunk(
        self,
        chunk: dict,
        default_chunk_class: type,
        base_generation_info: dict | None,
    ) -> ChatGenerationChunk | None:
        """Override to capture reasoning_content from streaming deltas.

        DeepSeek's API returns reasoning_content as a top-level field in the delta
        (alongside content), but the base langchain_openai implementation ignores it.
        We capture it here and store it in additional_kwargs so it persists in state.
        """
        # First, let the parent handle the standard conversion
        result = super()._convert_chunk_to_generation_chunk(
            chunk, default_chunk_class, base_generation_info
        )

        if result is None:
            return None

        # Check if the delta contains reasoning_content
        choices = (
            chunk.get("choices", [])
            or chunk.get("chunk", {}).get("choices", [])
        )
        if not choices:
            return result

        delta = choices[0].get("delta")
        if delta is None:
            return result

        reasoning_content = delta.get("reasoning_content")
        if reasoning_content and isinstance(result.message, AIMessageChunk):
            # Store reasoning_content in additional_kwargs for this chunk.
            # LangChain's AIMessageChunk.__add__ will automatically concatenate
            # string values in additional_kwargs across chunks.
            result.message.additional_kwargs["reasoning_content"] = reasoning_content

        return result
