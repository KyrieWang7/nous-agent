"""Core modules for the LangGraph-compatible API server.

Key components:
- event_bus: Global async EventBus decoupling agent workers from SSE endpoints
- task_registry: Process-level background task lifecycle management
- redis_stream: Optional Redis Streams persistent event store
- stream: LangGraph astream chunk → SSE event transformer
- events: SSE formatting utilities
- state: In-memory thread/run state cache
- serializers: Message/state serialization
"""
