"""Ingest model preference resolver — 从 identity 拉任务 owner 的个人
ingest 模型偏好。

模型解析链中介于 env 显式覆盖与 relay default-chat 之间的一级
（runner._resolve_model）::

    GET <identity>/v1/internal/settings/{owner_id}/ingest-model
    Authorization: Bearer <BIUMIND_RELAY_INTERNAL_TOKEN>
    200 → {"model": "<model code>"}   404 → 用户未设置偏好

token 复用 relay 内部车道同一把共享密钥（identity 的
IDENTITY_INTERNAL_TOKEN 与之同值，compose 已对齐），不新增密钥。

与 default_model.py 的差异：缓存是 **per-owner** 的（不同任务 owner
各有偏好），命中缓存 60s TTL，失败（404 / 5xx / 网络）负缓存 10s —
— 偏好拉不到不该阻塞任务，由调用方回落解析链下一级。
identity_url / token 任一空 → resolver 禁用（``preference_model``
恒返 ""），BIUMIND_IDENTITY_URL 默认空即整层关闭（向后兼容）。
"""

from __future__ import annotations

import logging
import time
from typing import Callable, Dict, Optional, Tuple

import httpx


logger = logging.getLogger("biumind.wiki_llm.preference_model")


class PreferenceModelResolver:
    """按上面文档的契约查 owner 的 ingest 模型偏好并做 per-owner 缓存。

    ``transport`` / ``clock`` 仅供单测注入（httpx.MockTransport /
    可控时钟）；生产用默认值。TTL 是实例属性而非模块常量，方便单测缩短。
    """

    def __init__(
        self,
        identity_url: str,
        token: str,
        *,
        cache_ttl_s: float = 60.0,
        negative_ttl_s: float = 10.0,
        timeout_s: float = 5.0,
        transport: Optional[httpx.AsyncBaseTransport] = None,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self._identity_url = identity_url.rstrip("/")
        self._token = token
        self._cache_ttl = cache_ttl_s
        self._negative_ttl = negative_ttl_s
        self._timeout = timeout_s
        self._transport = transport
        self._clock = clock

        # per-owner：owner_id → (model, 命中缓存过期时刻)
        self._cached: Dict[str, Tuple[str, float]] = {}
        # per-owner 负缓存：owner_id → 负缓存过期时刻
        self._neg: Dict[str, float] = {}

    async def preference_model(self, owner_id: str) -> str:
        """返回 owner 设的 ingest 模型 code；未设置 / identity 不可达 /
        resolver 未启用时返 ""（由调用方回落解析链下一级）。"""
        if not self._identity_url or not self._token or not owner_id:
            return ""
        now = self._clock()
        hit = self._cached.get(owner_id)
        if hit is not None and now < hit[1]:
            return hit[0]
        if now < self._neg.get(owner_id, 0.0):
            return ""

        # 并发下允许多个任务同时 fetch（last write wins）—— 与
        # default_model.py 一样不做 singleflight，负缓存兜底保证
        # 不会持续打爆 identity。
        m = await self._fetch(owner_id)
        if m:
            self._cached[owner_id] = (m, self._clock() + self._cache_ttl)
            self._neg.pop(owner_id, None)
            return m
        # 负缓存：404（用户未设置偏好）与 5xx / 网络错误同待遇。
        self._neg[owner_id] = self._clock() + self._negative_ttl
        return ""

    async def _fetch(self, owner_id: str) -> str:
        """打一次 identity internal 端点。任何失败（含 404）都返 ""。"""
        url = (self._identity_url
               + f"/v1/internal/settings/{owner_id}/ingest-model")
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
            logger.warning(
                "preference model resolver: identity unreachable: %s", e,
            )
            return ""
        if resp.status_code == 404:
            # 用户未设置偏好是合法状态，由调用方负缓存。
            return ""
        if resp.status_code != 200:
            logger.warning(
                "preference model resolver: unexpected status %d",
                resp.status_code,
            )
            return ""
        try:
            model = resp.json().get("model", "")
        except (ValueError, AttributeError) as e:
            logger.warning("preference model resolver: bad payload: %s", e)
            return ""
        return model if isinstance(model, str) else ""
