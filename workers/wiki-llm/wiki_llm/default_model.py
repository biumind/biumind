"""Default chat model resolver — 从 model-relay 解析平台默认 chat 模型。

Python 移植 brain 的 ``agentplane.DefaultModelResolver``
(services/brain/internal/agentplane/default_model.go),契约一致::

    GET <relay>/v1/internal/models/default-chat
    Authorization: Bearer <BIUMIND_RELAY_INTERNAL_TOKEN>
    200 → {"code": "<model code>"}    404 → admin 未配默认模型
    503 → relay registry cache 未就绪

真相源是 relay ``models.is_default_chat``(admin 在后台指定);2026-09-01
事故后 worker 不再硬编码唯一模型 —— 硬编码的 code 与生产命名规范
(带 provider 前缀)不一致时 relay 直接 502 model_disabled。

缓存策略与 brain 完全对齐:命中缓存 60s TTL;失败(404 / 5xx / 网络)
负缓存 10s —— relay 短暂不可用时每个任务都重试,但不打爆 relay。
relay_url / token 任一空 → resolver 禁用(``default_chat_model``
恒返 "")。调用方拿到 "" 后落自己的兜底链(runner._resolve_model)。
"""

from __future__ import annotations

import logging
import time
from typing import Callable, Optional

import httpx


logger = logging.getLogger("biumind.wiki_llm.default_model")


class DefaultModelResolver:
    """按上面文档的契约查 relay 默认 chat model 并缓存。

    ``transport`` / ``clock`` 仅供单测注入(httpx.MockTransport /
    可控时钟);生产用默认值。TTL 是实例属性而非模块常量,方便单测缩短。
    """

    def __init__(
        self,
        relay_url: str,
        token: str,
        *,
        cache_ttl_s: float = 60.0,
        negative_ttl_s: float = 10.0,
        timeout_s: float = 5.0,
        transport: Optional[httpx.AsyncBaseTransport] = None,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self._relay_url = relay_url.rstrip("/")
        self._token = token
        self._cache_ttl = cache_ttl_s
        self._negative_ttl = negative_ttl_s
        self._timeout = timeout_s
        self._transport = transport
        self._clock = clock

        self._cached = ""
        self._cache_exp = 0.0
        self._neg_exp = 0.0

    async def warm(self) -> None:
        """启动时异步预热缓存(对齐 brain main.go ``go resolver.Warm``),
        让第一个任务不付 relay 往返。失败静默 —— 下个任务会重试。"""
        await self.default_chat_model()

    async def default_chat_model(self) -> str:
        """返回 relay 配的默认 chat model code;未配 / relay 不可达 /
        resolver 未启用时返 ""(由调用方落兜底链)。"""
        if not self._relay_url or not self._token:
            return ""
        now = self._clock()
        if self._cached and now < self._cache_exp:
            return self._cached
        if now < self._neg_exp:
            return ""

        # 并发下允许多个任务同时 fetch(last write wins)—— 与 brain 一样
        # 不做 singleflight,负缓存兜底保证不会持续打爆 relay。
        m = await self._fetch()
        if m:
            self._cached = m
            self._cache_exp = self._clock() + self._cache_ttl
            self._neg_exp = 0.0
            return m
        # 负缓存:404(admin 未配默认模型)与 5xx / 网络错误同待遇。
        self._neg_exp = self._clock() + self._negative_ttl
        return ""

    async def _fetch(self) -> str:
        """打一次 relay internal 端点。任何失败(含 404)都返 ""。"""
        url = self._relay_url + "/v1/internal/models/default-chat"
        headers = {"Authorization": f"Bearer {self._token}"}
        timeout = httpx.Timeout(
            connect=self._timeout, read=self._timeout,
            write=self._timeout, pool=self._timeout,
        )
        try:
            async with httpx.AsyncClient(
                timeout=timeout, transport=self._transport,
            ) as client:
                resp = await client.get(url, headers=headers)
        except httpx.HTTPError as e:
            logger.warning("default model resolver: relay unreachable: %s", e)
            return ""
        if resp.status_code == 404:
            # admin 未配默认模型是合法状态,由调用方负缓存。
            return ""
        if resp.status_code != 200:
            logger.warning(
                "default model resolver: unexpected status %d",
                resp.status_code,
            )
            return ""
        try:
            code = resp.json().get("code", "")
        except (ValueError, AttributeError) as e:
            logger.warning("default model resolver: bad payload: %s", e)
            return ""
        return code if isinstance(code, str) else ""
