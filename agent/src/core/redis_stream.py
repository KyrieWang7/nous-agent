"""
RedisEventStream — 基于 Redis Streams 的持久化事件流

支持：
- 持久化：事件写入 Redis Stream，不会因 Pod 重启丢失
- 断线重连：SSE 重连时从 Last-Event-ID 继续读取
- TTL 自动过期：Stream 条目自动淘汰，不无限膨胀

Stream key 格式：
  nous:run:events:{run_id}

每个 entry 包含：
  event: SSE 事件名
  data: JSON 编码的事件负载

使用方式：
    from src.core.redis_stream import RedisEventStream

    redis_stream = RedisEventStream(redis_client)
    get_event_bus().enable_redis_stream(redis_stream)
"""

from __future__ import annotations

import json
import logging
from typing import Any, AsyncIterator

logger = logging.getLogger(__name__)

STREAM_PREFIX = "nous:run:events:"
STREAM_MAXLEN = 2000  # 每个 run 最多保留 2000 条事件
STREAM_TTL_SECONDS = 3600  # 1 小时后自动过期


class RedisEventStream:
    """基于 Redis Streams 的事件持久化层。"""

    def __init__(self, redis_client: Any) -> None:
        """
        Args:
            redis_client: redis.asyncio.Redis 实例
        """
        self._redis = redis_client

    async def publish(self, run_id: str, event: str, data: Any) -> str | None:
        """发布事件到 Redis Stream。

        Returns:
            Stream entry ID（如 "1234567890-0"），失败返回 None
        """
        stream_key = f"{STREAM_PREFIX}{run_id}"
        try:
            payload = json.dumps(data, default=str, ensure_ascii=False)
            entry_id = await self._redis.xadd(
                stream_key,
                {"event": event, "data": payload},
                maxlen=STREAM_MAXLEN,
                approximate=True,
            )
            # 设置 TTL（幂等，每次写入刷新）
            await self._redis.expire(stream_key, STREAM_TTL_SECONDS)
            return entry_id.decode() if isinstance(entry_id, bytes) else str(entry_id)
        except Exception as e:
            logger.debug("RedisEventStream.publish failed: %s", e)
            return None

    async def read(
        self,
        run_id: str,
        last_id: str = "0",
        count: int = 100,
        block_ms: int = 2000,
    ) -> list[dict[str, Any]]:
        """从 Redis Stream 读取事件。

        Args:
            run_id: 目标 run ID
            last_id: 起始 entry ID（"0" = 从头读, "$" = 仅新消息）
            count: 每次读取的最大条数
            block_ms: 阻塞等待时间（毫秒），0 = 不阻塞

        Returns:
            事件列表 [{"entry_id": str, "event": str, "data": Any}]
        """
        stream_key = f"{STREAM_PREFIX}{run_id}"
        try:
            entries = await self._redis.xread(
                {stream_key: last_id},
                count=count,
                block=block_ms if block_ms > 0 else None,
            )
            if not entries:
                return []

            results = []
            for _stream, messages in entries:
                for entry_id, fields in messages:
                    eid = entry_id.decode() if isinstance(entry_id, bytes) else str(entry_id)
                    event_name = _decode_field(fields, "event")
                    data_raw = _decode_field(fields, "data")
                    try:
                        data = json.loads(data_raw) if data_raw else {}
                    except (json.JSONDecodeError, TypeError):
                        data = {}
                    results.append({
                        "entry_id": eid,
                        "event": event_name,
                        "data": data,
                    })
            return results
        except Exception as e:
            logger.debug("RedisEventStream.read failed: %s", e)
            return []

    async def read_after(self, run_id: str, last_event_id: str) -> list[dict[str, Any]]:
        """读取指定 ID 之后的所有事件（用于断线重连回放）。

        Args:
            run_id: 目标 run ID
            last_event_id: 客户端最后收到的事件 ID

        Returns:
            last_event_id 之后的所有事件
        """
        return await self.read(run_id, last_id=last_event_id, count=STREAM_MAXLEN, block_ms=0)

    async def read_all(self, run_id: str) -> list[dict[str, Any]]:
        """读取 run 的所有事件（从头开始）。"""
        return await self.read(run_id, last_id="0", count=STREAM_MAXLEN, block_ms=0)

    async def stream_sse(
        self,
        run_id: str,
        last_id: str = "$",
    ) -> AsyncIterator[str]:
        """生成 SSE 格式的事件流（用于 StreamingResponse）。

        Yields:
            SSE 格式字符串: "id: {entry_id}\\nevent: {event}\\ndata: {json}\\n\\n"
        """
        current_id = last_id
        while True:
            events = await self.read(run_id, last_id=current_id, count=50, block_ms=2000)
            if not events:
                yield ": keepalive\n\n"
                continue
            for event in events:
                eid = event["entry_id"]
                etype = event["event"]
                data = json.dumps(event["data"], ensure_ascii=False, default=str)
                yield f"id: {eid}\nevent: {etype}\ndata: {data}\n\n"
                current_id = eid
                # 检查是否收到 end 事件
                if etype == "end":
                    return

    async def cleanup(self, run_id: str) -> None:
        """清理 run 的事件 stream。"""
        stream_key = f"{STREAM_PREFIX}{run_id}"
        try:
            await self._redis.delete(stream_key)
        except Exception as e:
            logger.debug("RedisEventStream.cleanup failed: %s", e)


def _decode_field(fields: dict, key: str) -> str:
    """从 Redis Stream entry 中解码字段值。"""
    val = fields.get(key.encode(), fields.get(key, b""))
    if isinstance(val, bytes):
        return val.decode("utf-8")
    return str(val) if val else ""
