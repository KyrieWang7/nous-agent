"""
EventBus — 全局异步事件总线

设计参考 Koda 的 AgentEventBus，适配 nous-agent 的 LangGraph 架构。

三条数据通路：
1. Queue 通路 → SSE 实时推送（允许丢失，重连后从 checkpoint/Redis 补）
2. Buffer 通路 → 内存 deque 保留最近 N 条事件（短时间重连可回放）
3. Redis Stream 通路 → 持久化事件流（可选，启用后支持 Last-Event-ID 精确恢复）

设计约束：
- publish() 非阻塞，不影响 Agent Graph 执行
- Queue 满时丢弃最旧事件（SSE 允许丢失）
- Redis 写失败降级为仅内存，不阻塞主流程
- 支持同一 run_id 多个 SSE 订阅者（多端/多窗口）
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from collections import deque
from dataclasses import dataclass, field
from typing import Any

logger = logging.getLogger(__name__)

# Queue 容量（每个订阅者）
_QUEUE_MAX_SIZE = 500

# 事件缓冲区容量（每个 run）
_BUFFER_MAX_SIZE = 500


@dataclass(frozen=True)
class StreamEvent:
    """单条事件，带有序号用于断线重连。

    Attributes:
        id: 单调递增的事件 ID（时间戳-序号格式，兼容 SSE id: 字段）
        event: SSE event 名称（values, messages, custom, error, end）
        data: JSON 可序列化的事件负载
        run_id: 所属的 run ID
    """

    id: str
    event: str
    data: Any
    run_id: str


class EventBus:
    """全局事件总线 — 解耦 Agent Worker 和 SSE 消费者。

    Usage:
        bus = get_event_bus()

        # 生产者（Agent 执行侧）
        await bus.publish(run_id, "values", {...})
        await bus.publish_end(run_id)

        # 消费者（SSE 端点侧）
        queue = bus.subscribe(run_id)
        ...
        bus.unsubscribe(run_id, queue)
    """

    def __init__(self) -> None:
        # SSE 订阅者（key=run_id, value=Queue 列表）
        self._subscribers: dict[str, list[asyncio.Queue]] = {}

        # 事件缓冲区（key=run_id, value=deque of StreamEvent）
        self._buffers: dict[str, deque[StreamEvent]] = {}

        # 序号计数器（key=run_id）
        self._counters: dict[str, int] = {}

        # Redis Stream 双写（可选）
        self._redis_stream: Any = None

    def enable_redis_stream(self, redis_stream: Any) -> None:
        """启用 Redis Streams 双写（生产环境推荐）。"""
        self._redis_stream = redis_stream
        logger.info("EventBus: Redis Stream dual-write enabled")

    # ==================== 事件 ID 生成 ====================

    def _next_id(self, run_id: str) -> str:
        """生成单调递增的事件 ID（格式: {timestamp_ms}-{seq}）"""
        self._counters[run_id] = self._counters.get(run_id, 0) + 1
        ts = int(time.time() * 1000)
        seq = self._counters[run_id] - 1
        return f"{ts}-{seq}"

    # ==================== 发布 ====================

    async def publish(self, run_id: str, event: str, data: Any) -> str:
        """发布一条事件到指定 run 的事件流。

        Args:
            run_id: 目标 run ID
            event: SSE 事件名（values, messages, custom, error, etc.）
            data: 事件数据（JSON 可序列化）

        Returns:
            事件 ID
        """
        event_id = self._next_id(run_id)
        entry = StreamEvent(id=event_id, event=event, data=data, run_id=run_id)

        # 1. 写入内存缓冲区（用于短时间内的重连回放）
        buf = self._buffers.setdefault(run_id, deque(maxlen=_BUFFER_MAX_SIZE))
        buf.append(entry)

        # 2. 推送到所有 SSE 订阅者队列（非阻塞）
        if run_id in self._subscribers:
            for queue in self._subscribers[run_id]:
                try:
                    if queue.full():
                        # 丢弃最旧事件，不阻塞
                        try:
                            queue.get_nowait()
                        except asyncio.QueueEmpty:
                            pass
                    queue.put_nowait(entry)
                except Exception:
                    pass  # 不阻塞 Agent 执行

        # 3. Redis Stream 双写（非阻塞，失败降级）
        if self._redis_stream:
            try:
                await self._redis_stream.publish(run_id, event, data)
            except Exception as e:
                logger.debug("Redis Stream write failed (degraded): %s", e)

        return event_id

    async def publish_end(self, run_id: str) -> None:
        """发送 run 结束信号。"""
        await self.publish(run_id, "end", {})

    # ==================== 订阅 ====================

    def subscribe(self, run_id: str) -> asyncio.Queue:
        """订阅 run 的事件流，返回一个 asyncio.Queue。

        调用方从 Queue 中读取 StreamEvent 对象。
        多次调用可创建多个独立订阅者（多端同时订阅）。
        """
        queue: asyncio.Queue[StreamEvent] = asyncio.Queue(maxsize=_QUEUE_MAX_SIZE)
        if run_id not in self._subscribers:
            self._subscribers[run_id] = []
        self._subscribers[run_id].append(queue)
        logger.debug("EventBus: subscriber added for run %s (total=%d)",
                     run_id, len(self._subscribers[run_id]))
        return queue

    def unsubscribe(self, run_id: str, queue: asyncio.Queue) -> None:
        """取消订阅。"""
        subs = self._subscribers.get(run_id, [])
        if queue in subs:
            subs.remove(queue)
        if not subs:
            self._subscribers.pop(run_id, None)
        logger.debug("EventBus: subscriber removed for run %s", run_id)

    # ==================== 事件回放 ====================

    def get_buffered_events(
        self,
        run_id: str,
        after_id: str | None = None,
    ) -> list[StreamEvent]:
        """获取缓冲区中的事件（用于重连回放）。

        Args:
            run_id: 目标 run ID
            after_id: 可选，仅返回此 ID 之后的事件（Last-Event-ID 语义）

        Returns:
            事件列表（按时间顺序）
        """
        buf = self._buffers.get(run_id)
        if not buf:
            return []

        if after_id is None:
            return list(buf)

        # 从 after_id 之后开始返回
        found = False
        result = []
        for entry in buf:
            if found:
                result.append(entry)
            elif entry.id == after_id:
                found = True

        return result

    # ==================== 清理 ====================

    async def cleanup(self, run_id: str, *, delay: float = 60) -> None:
        """延迟清理 run 相关资源。

        给断线重连留出时间窗口，延迟后清除缓冲区和订阅者。
        """
        if delay > 0:
            await asyncio.sleep(delay)

        self._buffers.pop(run_id, None)
        self._counters.pop(run_id, None)
        # 清理残留订阅者
        self._subscribers.pop(run_id, None)

        # Redis Stream 清理（可选，通常靠 TTL 自动过期）
        if self._redis_stream:
            try:
                await self._redis_stream.cleanup(run_id)
            except Exception:
                pass

        logger.debug("EventBus: cleaned up run %s", run_id)

    def get_subscriber_count(self, run_id: str) -> int:
        """获取指定 run 的订阅者数量。"""
        return len(self._subscribers.get(run_id, []))

    def get_active_runs(self) -> list[str]:
        """获取有活跃缓冲区的 run ID 列表。"""
        return list(self._buffers.keys())


# ==================== 全局单例 ====================

_event_bus: EventBus | None = None


def get_event_bus() -> EventBus:
    """获取全局 EventBus 单例。"""
    global _event_bus
    if _event_bus is None:
        _event_bus = EventBus()
    return _event_bus


def reset_event_bus() -> None:
    """重置（仅测试用）。"""
    global _event_bus
    _event_bus = None
