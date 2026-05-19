"""Run streaming and management endpoints (LangGraph Platform API).

Architecture (EventBus-driven):
- POST /threads/{id}/runs/stream  → 创建 run + 后台 task + SSE 消费
- GET  /threads/{id}/runs/{rid}/stream → 断线重连（join 正在进行的 run）
- POST /threads/{id}/runs/{rid}/cancel → 取消 run

SSE 断线后 Agent 任务继续在后台运行。前端重连时：
1. 先检查 TaskRegistry 中任务是否存活
2. 存活 → subscribe EventBus 实时队列 + 回放缓冲区中的历史事件
3. 已结束 → 从 checkpoint 加载最终状态
"""

from __future__ import annotations

import asyncio
import json
import logging
import uuid
from typing import Any

from fastapi import APIRouter, HTTPException, Header, Request
from pydantic import BaseModel, Field
from sse_starlette.sse import EventSourceResponse, ServerSentEvent

from src.agents.lead_agent.agent import make_lead_agent
from src.core.event_bus import StreamEvent, get_event_bus
from src.core.serializers import serialize_state_values
from src.core.state import get_runs, get_threads, now_iso, thread_dict
from src.core.stream import estimate_token_usage, transform_astream_chunk
from src.core.task_registry import (
    cancel_task,
    get as get_task,
    get_by_thread,
    is_running,
    register,
    unregister,
)
from src.storage.database import get_checkpointer

logger = logging.getLogger(__name__)

router = APIRouter(tags=["Runs"])

HEARTBEAT_INTERVAL = 15  # seconds


async def _ensure_swarm_team(thread_id: str, configurable: dict) -> None:
    """Auto-create a swarm team if swarm_enabled and no team exists for this thread."""
    import uuid as _uuid

    try:
        tid = _uuid.UUID(thread_id)
    except (ValueError, AttributeError):
        logger.warning("Swarm: thread_id '%s' is not a valid UUID, skipping auto-team", thread_id)
        return

    try:
        from src.swarm.schema import setup_swarm_tables
        await setup_swarm_tables()

        from src.storage.database import get_db_connection
        async with get_db_connection() as conn:
            existing = await conn.fetchrow(
                "SELECT id, name FROM swarm_teams WHERE lead_thread_id = $1 LIMIT 1",
                tid,
            )
            if existing:
                configurable["swarm_team_id"] = str(existing["id"])
                configurable["swarm_team_name"] = existing["name"]
                return

        from src.swarm.team import TeamManager
        manager = TeamManager()
        team = await manager.create_team(f"team-{thread_id[:8]}", "Auto-created swarm team", thread_id)
        configurable["swarm_team_id"] = team.id
        configurable["swarm_team_name"] = team.name
    except Exception as e:
        logger.warning("Failed to auto-create swarm team: %s", e)


class RunCreate(BaseModel):
    model_config = {"populate_by_name": True}

    assistant_id: str | None = "lead_agent"
    input: dict[str, Any] | list | str | None = None
    command: dict[str, Any] | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)
    config: dict[str, Any] = Field(default_factory=dict)
    context: dict[str, Any] = Field(default_factory=dict)
    stream_mode: list[str] = Field(default_factory=lambda: ["values"], alias="streamMode")
    stream_subgraphs: bool = Field(default=False, alias="streamSubgraphs")
    stream_resumable: bool = False
    on_disconnect: str = "cancel"


def _json_str(data: Any) -> str:
    return json.dumps(data, default=str, ensure_ascii=False)


def _event_to_sse(entry: StreamEvent) -> ServerSentEvent:
    """Convert a StreamEvent to a ServerSentEvent with id field."""
    return ServerSentEvent(
        data=_json_str(entry.data),
        event=entry.event,
        id=entry.id,
    )


# ---------------------------------------------------------------------------
# Background agent execution (producer)
# ---------------------------------------------------------------------------


async def _run_agent_task(
    run_id: str,
    thread_id: str,
    runnable_config: dict,
    graph_input: Any,
    stream_mode: list[str],
    stream_subgraphs: bool,
    context: dict,
) -> None:
    """Background task: execute the agent graph and publish events to EventBus.

    This task runs independently of the SSE connection. If the client
    disconnects, the task continues running to completion.
    """
    bus = get_event_bus()
    runs = get_runs()

    try:
        runs[run_id]["status"] = "running"
        checkpointer = await get_checkpointer()
        compiled = make_lead_agent(runnable_config, checkpointer=checkpointer)

        async for chunk in compiled.astream(
            graph_input,
            config=runnable_config,
            context=context,
            stream_mode=stream_mode,
            subgraphs=stream_subgraphs,
        ):
            sse = transform_astream_chunk(chunk, stream_mode, stream_subgraphs)
            if sse:
                await bus.publish(run_id, sse["event"], sse["data"])
                # Token usage as custom event
                if sse["event"] == "values" and "custom" in stream_mode:
                    token_sse = estimate_token_usage(sse["data"])
                    if token_sse:
                        await bus.publish(run_id, token_sse["event"], token_sse["data"])

        runs[run_id]["status"] = "success"

    except asyncio.CancelledError:
        runs[run_id]["status"] = "cancelled"
        logger.info("Run %s cancelled", run_id)

    except Exception as exc:
        runs[run_id]["status"] = "error"
        logger.exception("Run %s failed: %s", run_id, exc)
        await bus.publish(run_id, "error", {"message": str(exc), "type": type(exc).__name__})

    finally:
        # 发送 end 信号并延迟清理
        await bus.publish_end(run_id)
        unregister(run_id)
        asyncio.create_task(bus.cleanup(run_id, delay=120))


# ---------------------------------------------------------------------------
# SSE consumer (subscriber)
# ---------------------------------------------------------------------------


async def _sse_consumer(
    run_id: str,
    *,
    replay_after: str | None = None,
):
    """Async generator that yields ServerSentEvent from EventBus.

    Args:
        run_id: The run to subscribe to.
        replay_after: Optional Last-Event-ID. If provided, replay buffered
                      events after this ID before live streaming.
    """
    bus = get_event_bus()

    # 1. Emit metadata
    yield ServerSentEvent(data=_json_str({"run_id": run_id}), event="metadata")

    # 2. Replay buffered events (for reconnection)
    if replay_after:
        for entry in bus.get_buffered_events(run_id, after_id=replay_after):
            yield _event_to_sse(entry)
            if entry.event == "end":
                return
    else:
        # 新连接也回放所有缓冲事件（适用于 join 已进行中的 run）
        for entry in bus.get_buffered_events(run_id):
            yield _event_to_sse(entry)
            if entry.event == "end":
                return

    # 3. Subscribe for live events
    queue = bus.subscribe(run_id)
    try:
        while True:
            try:
                entry: StreamEvent = await asyncio.wait_for(
                    queue.get(), timeout=HEARTBEAT_INTERVAL * 8
                )
            except asyncio.TimeoutError:
                # 超时但任务仍在运行 → 继续等待
                if is_running(run_id):
                    continue
                # 任务已结束，退出
                break

            yield _event_to_sse(entry)

            if entry.event == "end":
                return

    except asyncio.CancelledError:
        # SSE 断开 — 不杀后台任务
        logger.info("SSE consumer disconnected for run %s", run_id)

    finally:
        bus.unsubscribe(run_id, queue)


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------


@router.post("/threads/{thread_id}/runs/stream")
async def stream_run(thread_id: str, body: RunCreate):
    """Create a run, launch background task, and stream events via SSE."""
    threads = get_threads()
    runs = get_runs()
    bus = get_event_bus()

    if thread_id not in threads:
        threads[thread_id] = thread_dict(thread_id)

    run_id = str(uuid.uuid4())
    runs[run_id] = {
        "run_id": run_id,
        "thread_id": thread_id,
        "assistant_id": body.assistant_id,
        "status": "pending",
        "created_at": now_iso(),
        "metadata": body.metadata,
        "on_disconnect": body.on_disconnect,
    }

    configurable = body.config.get("configurable", {})
    configurable["thread_id"] = thread_id
    if body.context:
        configurable.update(body.context)

    if configurable.get("swarm_enabled"):
        await _ensure_swarm_team(thread_id, configurable)

    runnable_config = {
        "configurable": configurable,
        "metadata": body.metadata,
        "recursion_limit": body.config.get("recursion_limit", 1000),
    }

    stream_mode = body.stream_mode or ["values"]

    graph_input = body.input
    if body.command:
        from langgraph.types import Command
        graph_input = Command(**body.command)

    ctx = body.context or {}
    ctx["thread_id"] = thread_id

    # Launch background agent task
    task = asyncio.create_task(
        _run_agent_task(
            run_id=run_id,
            thread_id=thread_id,
            runnable_config=runnable_config,
            graph_input=graph_input,
            stream_mode=stream_mode,
            stream_subgraphs=body.stream_subgraphs,
            context=ctx,
        )
    )
    register(run_id, thread_id, task, on_disconnect=body.on_disconnect)

    # Return SSE response (consumer)
    return EventSourceResponse(
        _sse_consumer(run_id),
        ping=HEARTBEAT_INTERVAL,
        headers={
            "X-Accel-Buffering": "no",
            "Content-Location": f"/threads/{thread_id}/runs/{run_id}/stream",
        },
    )


@router.get("/threads/{thread_id}/runs")
async def list_runs(thread_id: str):
    return [r for r in get_runs().values() if r.get("thread_id") == thread_id]


@router.get("/threads/{thread_id}/runs/{run_id}")
async def get_run(thread_id: str, run_id: str):
    run = get_runs().get(run_id)
    if not run:
        raise HTTPException(status_code=404, detail=f"Run {run_id} not found")
    return run


@router.get("/threads/{thread_id}/runs/{run_id}/stream")
async def reconnect_run_stream(
    thread_id: str,
    run_id: str,
    request: Request,
    last_event_id: str | None = Header(None, alias="Last-Event-ID"),
):
    """Reconnect to an existing run's event stream.

    Supports three scenarios:
    1. Task still running → replay buffered events + subscribe for live events
    2. Task finished but buffer still alive → replay buffered events
    3. Task gone (restart) → return checkpoint state as 'values' event
    """
    bus = get_event_bus()

    # Scenario 1 & 2: task running or buffer still available
    if is_running(run_id) or bus.get_buffered_events(run_id):
        return EventSourceResponse(
            _sse_consumer(run_id, replay_after=last_event_id),
            ping=HEARTBEAT_INTERVAL,
            headers={"X-Accel-Buffering": "no"},
        )

    # Scenario 3: fallback to checkpoint
    async def _checkpoint_fallback():
        yield ServerSentEvent(data=_json_str({"run_id": run_id}), event="metadata")
        try:
            checkpointer = await get_checkpointer()
            cfg = {"configurable": {"thread_id": thread_id}}
            cp = await checkpointer.aget_tuple(cfg)
            if cp is not None:
                values = cp.checkpoint.get("channel_values", {})
                yield ServerSentEvent(
                    data=_json_str(serialize_state_values(values)),
                    event="values",
                )
        except Exception:
            logger.warning("Failed to load checkpoint for reconnect", exc_info=True)
        yield ServerSentEvent(data=_json_str({}), event="end")

    return EventSourceResponse(
        _checkpoint_fallback(),
        ping=HEARTBEAT_INTERVAL,
        headers={"X-Accel-Buffering": "no"},
    )


@router.post("/threads/{thread_id}/runs/{run_id}/cancel")
async def cancel_run_endpoint(thread_id: str, run_id: str):
    """Cancel a running or pending run."""
    run = get_runs().get(run_id)
    if run:
        run["status"] = "cancelled"

    cancelled = cancel_task(run_id)
    return {"ok": True, "was_running": cancelled}


@router.post("/runs/stream")
async def stateless_stream_run(body: RunCreate):
    """Stateless run — creates a temporary thread."""
    thread_id = str(uuid.uuid4())
    return await stream_run(thread_id, body)


@router.get("/threads/{thread_id}/stream")
async def thread_stream(thread_id: str):
    """Thread-level SSE stream.

    If the thread has a running task, join its event stream.
    Otherwise return empty stream.
    """
    task_handle = get_by_thread(thread_id)

    if task_handle and not task_handle.is_done:
        return EventSourceResponse(
            _sse_consumer(task_handle.run_id),
            ping=HEARTBEAT_INTERVAL,
            headers={"X-Accel-Buffering": "no"},
        )

    async def _empty():
        yield ServerSentEvent(data=_json_str({}), event="end")

    return EventSourceResponse(
        _empty(),
        headers={"X-Accel-Buffering": "no"},
    )
