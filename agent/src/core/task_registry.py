"""
TaskRegistry — 进程级后台任务生命周期管理

记录当前进程中正在运行的 Agent Graph 任务，支持：
- SSE 断开后任务继续运行（不被 cancel）
- 前端重连时查询任务是否存活，加入实时事件流
- 用户主动取消任务

设计参考 Koda 的 task_registry，简化适配 nous-agent：
- 进程级 dict（单实例部署足够）
- 多实例部署时改为 Redis Hash（Phase 2）
"""

from __future__ import annotations

import asyncio
import logging
import time
from dataclasses import dataclass, field

logger = logging.getLogger(__name__)


@dataclass
class TaskHandle:
    """运行中的任务句柄"""

    run_id: str
    thread_id: str
    task: asyncio.Task
    on_disconnect: str = "cancel"  # "cancel" | "continue"
    started_at: float = field(default_factory=time.time)

    @property
    def is_done(self) -> bool:
        return self.task.done()

    @property
    def elapsed_seconds(self) -> float:
        return time.time() - self.started_at

    def to_dict(self) -> dict:
        return {
            "run_id": self.run_id,
            "thread_id": self.thread_id,
            "started_at": self.started_at,
            "elapsed_seconds": round(self.elapsed_seconds, 1),
            "is_done": self.is_done,
            "on_disconnect": self.on_disconnect,
        }


# ── 全局注册表 ──

_TASKS: dict[str, TaskHandle] = {}


def register(
    run_id: str,
    thread_id: str,
    task: asyncio.Task,
    *,
    on_disconnect: str = "cancel",
) -> TaskHandle:
    """注册一个正在运行的任务。"""
    handle = TaskHandle(
        run_id=run_id,
        thread_id=thread_id,
        task=task,
        on_disconnect=on_disconnect,
    )
    _TASKS[run_id] = handle
    logger.info("Task registered: run_id=%s thread_id=%s", run_id, thread_id)
    return handle


def unregister(run_id: str) -> bool:
    """注销任务（任务完成/失败时调用）。"""
    removed = _TASKS.pop(run_id, None)
    if removed:
        logger.info(
            "Task unregistered: run_id=%s elapsed=%.1fs",
            run_id,
            removed.elapsed_seconds,
        )
        return True
    return False


def get(run_id: str) -> TaskHandle | None:
    """查询任务句柄。自动清理已完成但未注销的任务。"""
    handle = _TASKS.get(run_id)
    if handle and handle.is_done:
        _TASKS.pop(run_id, None)
        logger.debug("Stale task auto-cleaned: run_id=%s", run_id)
        return None
    return handle


def get_by_thread(thread_id: str) -> TaskHandle | None:
    """根据 thread_id 查询正在运行的任务。"""
    for handle in _TASKS.values():
        if handle.thread_id == thread_id and not handle.is_done:
            return handle
    return None


def is_running(run_id: str) -> bool:
    """判断指定 run 是否有正在运行的任务。"""
    return get(run_id) is not None


def is_thread_busy(thread_id: str) -> bool:
    """判断指定 thread 是否有正在运行的任务。"""
    return get_by_thread(thread_id) is not None


def cancel_task(run_id: str) -> bool:
    """取消指定 run 的任务。

    Returns:
        True 如果成功发送取消信号
    """
    handle = _TASKS.get(run_id)
    if not handle or handle.is_done:
        return False

    logger.info("Cancelling task: run_id=%s elapsed=%.1fs", run_id, handle.elapsed_seconds)
    handle.task.cancel()
    return True


def list_running() -> list[TaskHandle]:
    """列出所有运行中的任务（自动清理已完成的）。"""
    stale = [rid for rid, h in _TASKS.items() if h.is_done]
    for rid in stale:
        _TASKS.pop(rid, None)
    return list(_TASKS.values())
