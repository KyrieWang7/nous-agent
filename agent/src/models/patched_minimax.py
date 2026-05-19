import re
from typing import Any, AsyncIterator, Iterator, List, Optional, cast
from langchain_core.callbacks import AsyncCallbackManagerForLLMRun, CallbackManagerForLLMRun
from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, AIMessageChunk, BaseMessage
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult
from langchain_openai import ChatOpenAI


class PatchedChatMinimax(ChatOpenAI):
    """ChatOpenAI patched for Minimax models to extract <think> tags.
    
    Minimax returns its thinking process wrapped in <think>...</think> tags
    inside the main message content. Our frontend expects the thinking 
    process in `additional_kwargs["reasoning_content"]` instead of the text body.
    
    This patched class intercepts the API response, extracts the thinking text,
    and removes it from the main content so it can be rendered correctly as
    a reasoning bubble.
    """
    
    def _generate(
        self,
        messages: List[BaseMessage],
        stop: Optional[List[str]] = None,
        run_manager: Optional[CallbackManagerForLLMRun] = None,
        **kwargs: Any,
    ) -> ChatResult:
        """Call the base generate and then patch the results."""
        result = super()._generate(messages, stop=stop, run_manager=run_manager, **kwargs)
        
        for generation in result.generations:
            chat_generation = cast(ChatGeneration, generation)
            message = chat_generation.message
            
            if isinstance(message, AIMessage) and isinstance(message.content, str):
                content = message.content
                # Use regex to find <think>...</think> blocks (dotall for multiline)
                think_pattern = re.compile(r'<think>(.*?)</think>', re.DOTALL)
                match = think_pattern.search(content)
                
                if match:
                    reasoning = match.group(1).strip()
                    # Remove the think block from the main content
                    clean_content = think_pattern.sub('', content).strip()
                    
                    # Update message
                    message.content = clean_content
                    # Store reasoning where the frontend expects it
                    if "reasoning_content" not in message.additional_kwargs:
                        message.additional_kwargs["reasoning_content"] = reasoning
                else:
                    # Some versions might return unclosed <think> or just opening tags
                    # if the model generated malformed output or the user specifically prompted for it
                    if "<think>" in content:
                        parts = content.split("<think>", 1)
                        if len(parts) == 2:
                            after_think = parts[1]
                            if "</think>" in after_think:
                                reasoning, clean_content = after_think.split("</think>", 1)
                                message.content = parts[0] + clean_content
                                message.additional_kwargs["reasoning_content"] = reasoning.strip()

        return result

    async def _agenerate(
        self,
        messages: List[BaseMessage],
        stop: Optional[List[str]] = None,
        run_manager: Optional[AsyncCallbackManagerForLLMRun] = None,
        **kwargs: Any,
    ) -> ChatResult:
        """Call the base agenerate and then patch the results."""
        result = await super()._agenerate(messages, stop=stop, run_manager=run_manager, **kwargs)
        
        for generation in result.generations:
            chat_generation = cast(ChatGeneration, generation)
            message = chat_generation.message
            
            if isinstance(message, AIMessage) and isinstance(message.content, str):
                content = message.content
                import re
                think_pattern = re.compile(r'<think>(.*?)</think>', re.DOTALL)
                match = think_pattern.search(content)
                
                if match:
                    reasoning = match.group(1).strip()
                    clean_content = think_pattern.sub('', content).strip()
                    
                    message.content = clean_content
                    if "reasoning_content" not in message.additional_kwargs:
                        message.additional_kwargs["reasoning_content"] = reasoning
                else:
                    if "<think>" in content:
                        parts = content.split("<think>", 1)
                        if len(parts) == 2:
                            after_think = parts[1]
                            if "</think>" in after_think:
                                reasoning, clean_content = after_think.split("</think>", 1)
                                message.content = parts[0] + clean_content
                                message.additional_kwargs["reasoning_content"] = reasoning.strip()

        return result

    def _stream(
        self,
        messages: List[BaseMessage],
        stop: Optional[List[str]] = None,
        run_manager: Optional[CallbackManagerForLLMRun] = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        """Call the base stream and intercept chunks to parse <think> tags."""
        # pass run_manager=None to prevent base class from triggering callbacks with raw chunks
        chunks = super()._stream(messages, stop=stop, run_manager=None, **kwargs)

        in_think = False
        buffer = ""
        think_start_tag = "<think>"
        think_end_tag = "</think>"
        
        def _emit(emit_chunk: ChatGenerationChunk):
            if run_manager:
                content_str = emit_chunk.message.content if isinstance(emit_chunk.message.content, str) else ""
                run_manager.on_llm_new_token(content_str, chunk=emit_chunk)
            return emit_chunk

        for chunk in chunks:
            message = chunk.message
            if not isinstance(message, AIMessageChunk) or not isinstance(message.content, str) or not message.content:
                yield _emit(chunk)
                continue

            content = message.content

            if not in_think:
                buffer += content
                if think_start_tag in buffer:
                    parts = buffer.split(think_start_tag, 1)
                    # Before think tag -> emit as normal content
                    before_think = parts[0]
                    # After think tag -> this is reasoning
                    after_think = parts[1]
                    
                    in_think = True
                    buffer = after_think
                    
                    if before_think:
                        # Yield the content before <think>
                        message.content = before_think
                        yield _emit(chunk)
                    
                    # Also yield any reasoning that came in the same chunk after the tag
                    if buffer:
                        if think_end_tag in buffer:
                            r_parts = buffer.split(think_end_tag, 1)
                            reasoning = r_parts[0]
                            content_after = r_parts[1]
                            
                            in_think = False
                            buffer = content_after
                            
                            # Yield reasoning
                            reasoning_chunk = chunk.model_copy(deep=True)
                            reasoning_chunk.message.content = ""
                            reasoning_chunk.message.additional_kwargs = {"reasoning_content": reasoning}
                            yield _emit(reasoning_chunk)
                            
                            # If there's content after </think> in the SAME chunk, we buffered it, so wait for next loop or end
                        else:
                            # Still accumulating reasoning
                            reasoning_chunk = chunk.model_copy(deep=True)
                            reasoning_chunk.message.content = ""
                            reasoning_chunk.message.additional_kwargs = {"reasoning_content": buffer}
                            buffer = ""
                            yield _emit(reasoning_chunk)
                    continue
                else:
                    # Buffer is too short or doesn't have think tag (but might be the start of "<thi")
                    # To be safe and simple, let's just emit unless it ends with a partial tag
                    if "<" in buffer and len(buffer) - buffer.rfind("<") < len(think_start_tag):
                        # Might be forming a tag, hold it
                        continue
                    else:
                        # Not forming a tag, emit buffer
                        message.content = buffer
                        buffer = ""
                        yield _emit(chunk)
            else:
                # We are inside a thinking block
                buffer += content
                if think_end_tag in buffer:
                    parts = buffer.split(think_end_tag, 1)
                    reasoning = parts[0]
                    content_after = parts[1]
                    
                    in_think = False
                    buffer = content_after
                    
                    if reasoning:
                        reasoning_chunk = chunk.model_copy(deep=True)
                        reasoning_chunk.message.content = ""
                        reasoning_chunk.message.additional_kwargs = {"reasoning_content": reasoning}
                        yield _emit(reasoning_chunk)
                else:
                    # Check for partial end tag
                    if "<" in buffer and len(buffer) - buffer.rfind("<") < len(think_end_tag):
                        # Might be forming "</think>", emit everything except the potential partial tag
                        split_idx = buffer.rfind("<")
                        safe_reasoning = buffer[:split_idx]
                        buffer = buffer[split_idx:]
                        if safe_reasoning:
                            reasoning_chunk = chunk.model_copy(deep=True)
                            reasoning_chunk.message.content = ""
                            reasoning_chunk.message.additional_kwargs = {"reasoning_content": safe_reasoning}
                            yield _emit(reasoning_chunk)
                    else:
                        # Safe to emit all buffer as reasoning
                        reasoning_chunk = chunk.model_copy(deep=True)
                        reasoning_chunk.message.content = ""
                        reasoning_chunk.message.additional_kwargs = {"reasoning_content": buffer}
                        buffer = ""
                        yield _emit(reasoning_chunk)

        # Emit any remaining buffer at the end
        if buffer:
            # We want to use the last received chunk's metadata if possible, or an empty one
            last_chunk = chunk if 'chunk' in locals() else None
            gen_info = last_chunk.generation_info if last_chunk else None
            
            if in_think:
                # Still in think (unclosed tag), emit as reasoning
                msg = AIMessageChunk(content="", additional_kwargs={"reasoning_content": buffer})
                if last_chunk:
                    msg.response_metadata = last_chunk.message.response_metadata
                    msg.id = last_chunk.message.id
                final_chunk = ChatGenerationChunk(message=msg, generation_info=gen_info)
                yield _emit(final_chunk)
            else:
                # Normal content
                msg = AIMessageChunk(content=buffer)
                if last_chunk:
                    msg.response_metadata = last_chunk.message.response_metadata
                    msg.id = last_chunk.message.id
                final_chunk = ChatGenerationChunk(message=msg, generation_info=gen_info)
                yield _emit(final_chunk)

    async def _astream(
        self,
        messages: List[BaseMessage],
        stop: Optional[List[str]] = None,
        run_manager: Optional[AsyncCallbackManagerForLLMRun] = None,
        **kwargs: Any,
    ) -> AsyncIterator[ChatGenerationChunk]:
        """Call the base stream and intercept chunks to parse <think> tags."""
        chunks = super()._astream(messages, stop=stop, run_manager=None, **kwargs)

        in_think = False
        buffer = ""
        think_start_tag = "<think>"
        think_end_tag = "</think>"
        
        async def _aemit(emit_chunk: ChatGenerationChunk):
            if run_manager:
                content_str = emit_chunk.message.content if isinstance(emit_chunk.message.content, str) else ""
                await run_manager.on_llm_new_token(content_str, chunk=emit_chunk)
            return emit_chunk

        async for chunk in chunks:
            message = chunk.message
            if not isinstance(message, AIMessageChunk) or not isinstance(message.content, str) or not message.content:
                yield await _aemit(chunk)
                continue

            content = message.content

            if not in_think:
                buffer += content
                if think_start_tag in buffer:
                    parts = buffer.split(think_start_tag, 1)
                    before_think = parts[0]
                    after_think = parts[1]
                    
                    in_think = True
                    buffer = after_think
                    
                    if before_think:
                        message.content = before_think
                        yield await _aemit(chunk)
                    
                    if buffer:
                        if think_end_tag in buffer:
                            r_parts = buffer.split(think_end_tag, 1)
                            reasoning = r_parts[0]
                            content_after = r_parts[1]
                            
                            in_think = False
                            buffer = content_after
                            
                            reasoning_chunk = chunk.model_copy(deep=True)
                            reasoning_chunk.message.content = ""
                            reasoning_chunk.message.additional_kwargs = {"reasoning_content": reasoning}
                            yield await _aemit(reasoning_chunk)
                            
                        else:
                            reasoning_chunk = chunk.model_copy(deep=True)
                            reasoning_chunk.message.content = ""
                            reasoning_chunk.message.additional_kwargs = {"reasoning_content": buffer}
                            buffer = ""
                            yield await _aemit(reasoning_chunk)
                    continue
                else:
                    if "<" in buffer and len(buffer) - buffer.rfind("<") < len(think_start_tag):
                        continue
                    else:
                        message.content = buffer
                        buffer = ""
                        yield await _aemit(chunk)
            else:
                buffer += content
                if think_end_tag in buffer:
                    parts = buffer.split(think_end_tag, 1)
                    reasoning = parts[0]
                    content_after = parts[1]
                    
                    in_think = False
                    buffer = content_after
                    
                    if reasoning:
                        reasoning_chunk = chunk.model_copy(deep=True)
                        reasoning_chunk.message.content = ""
                        reasoning_chunk.message.additional_kwargs = {"reasoning_content": reasoning}
                        yield await _aemit(reasoning_chunk)
                else:
                    if "<" in buffer and len(buffer) - buffer.rfind("<") < len(think_end_tag):
                        split_idx = buffer.rfind("<")
                        safe_reasoning = buffer[:split_idx]
                        buffer = buffer[split_idx:]
                        if safe_reasoning:
                            reasoning_chunk = chunk.model_copy(deep=True)
                            reasoning_chunk.message.content = ""
                            reasoning_chunk.message.additional_kwargs = {"reasoning_content": safe_reasoning}
                            yield await _aemit(reasoning_chunk)
                    else:
                        reasoning_chunk = chunk.model_copy(deep=True)
                        reasoning_chunk.message.content = ""
                        reasoning_chunk.message.additional_kwargs = {"reasoning_content": buffer}
                        buffer = ""
                        yield await _aemit(reasoning_chunk)

        if buffer:
            last_chunk = chunk if 'chunk' in locals() else None
            gen_info = last_chunk.generation_info if last_chunk else None
            
            if in_think:
                msg = AIMessageChunk(content="", additional_kwargs={"reasoning_content": buffer})
                if last_chunk:
                    msg.response_metadata = last_chunk.message.response_metadata
                    msg.id = last_chunk.message.id
                final_chunk = ChatGenerationChunk(message=msg, generation_info=gen_info)
                yield await _aemit(final_chunk)
            else:
                msg = AIMessageChunk(content=buffer)
                if last_chunk:
                    msg.response_metadata = last_chunk.message.response_metadata
                    msg.id = last_chunk.message.id
                final_chunk = ChatGenerationChunk(message=msg, generation_info=gen_info)
                yield await _aemit(final_chunk)
