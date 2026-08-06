"""Provider 接口 — 异步生成任务的执行器.

抽象成两阶段:
  1. submit(task)        → external_task_id (上游异步 taskId)
  2. poll(external_id)   → Outcome (still_running | completed | failed)

worker 主循环按 poll_interval_s 反复 poll, 直到 terminal 状态.

错误分类:
  ProviderUnavailable    provider 未配 (空 API key 等); 调用方应让任务失败 + 退款
  ProviderError          上游业务错 (内容审核 / 限流 / 5xx); 调用方决定是否重试
"""

from __future__ import annotations

import abc
from dataclasses import dataclass, field
from typing import Optional


class ProviderError(Exception):
    """上游业务错或网络错."""


class ProviderUnavailable(ProviderError):
    """provider 未配置 (例如 DASHSCOPE_API_KEY 空) 或临时不可用."""


@dataclass
class Outcome:
    """poll 一次的结果."""

    status: str  # "running" | "completed" | "failed" | "blocked"
    progress: int = 0  # 0..100
    error_code: str = ""
    error_message: str = ""

    # 仅 status=completed 时填充: 上游产物的 URL 列表.
    # 由 worker 后续 storage.persist 转存到 MinIO.
    output_urls: list[str] = field(default_factory=list)
    # 单 output 的元信息 (可选): 顺序对应 output_urls.
    output_meta: list[dict] = field(default_factory=list)

    # 结构化产物 (非媒体): 爆款解析拆解结果 {copywriting, hooks, scenes, tags}.
    # 非空时 worker 不走 persist_url (没有媒体 URL 可下载), 而是直接构造一条
    # kind="hotparse" 的 OutputEntry, 把它内联进 metadata 落 task_outputs.
    structured: Optional[dict] = None


class Executor(abc.ABC):
    """Provider 执行器接口."""

    @property
    @abc.abstractmethod
    def code(self) -> str: ...

    @abc.abstractmethod
    async def submit(self, task) -> str:
        """提交任务, 返回 external_task_id. 失败抛 ProviderError/Unavailable."""

    @abc.abstractmethod
    async def poll(self, external_id: str) -> Outcome:
        """查询上游任务状态."""


# ─── Stub: 用于测试 / dev (不调真上游) ────────────────


class StubProvider(Executor):
    """立即完成的 stub provider, 返回 fake output URL.

    用法: SubmitTask.provider_code = "stub". 仅测试 / dev 模式启用.
    """

    @property
    def code(self) -> str:
        return "stub"

    async def submit(self, task) -> str:
        # 把 task_id 当 external_id 简化测试断言
        return f"stub-{task.task_id}"

    async def poll(self, external_id: str) -> Outcome:
        # 直接返回 completed
        return Outcome(
            status="completed",
            progress=100,
            output_urls=[f"https://stub.local/{external_id}.png"],
            output_meta=[{"width": 512, "height": 512, "mime_type": "image/png"}],
        )
