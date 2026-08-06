"""RelayProvider — 段 3.6: 生成流量经 model-relay 单一 egress.

不再由 worker 直连 dashscope/volcengine,而是调 model-relay 的内部端点
`POST /v1/internal/generations`(内部 bearer token + user_id)。model-relay
负责:解析模型 → 凭证解密 → 计费 Hold → 真·submit/poll 上游 → Settle,
同步返回产物 URL。worker 只剩「调 relay 拿 URL → 转存 MinIO」的瘦执行层。

适配 Executor 的 submit/poll 两阶段:model-relay 端点是同步的(一次调用
内部就把上游 submit+poll 走完),所以 submit 起一个后台 task 执行该阻塞
调用并立即返回,poll 查后台 task 状态 —— 这样 worker 主循环仍能发出
running 进度事件,完成后复用既有 MinIO 转存逻辑。

凭证:不再需要 task 里注入的 credential_*,也不读 DASHSCOPE_API_KEY —
model-relay 自己从 vault 解密。worker 仅持有调用 relay 的内部 token。
"""

from __future__ import annotations

import asyncio
import logging
from typing import Any, Optional

import httpx

from ..config import Config
from ..event import SubmitTask
from .base import Executor, Outcome, ProviderError, ProviderUnavailable


logger = logging.getLogger("biumind.aigc.relay")

GENERATE_PATH = "/v1/internal/generations"

# 哪些 provider_code 走 relay egress(model-relay 已具备对应 adaptor)。
# dashscope(wanx image/video)+ volcengine(Seedream image / Seedance video)。
RELAY_PROVIDERS: set[str] = {"dashscope", "volcengine"}


class RelayProvider(Executor):
    """经 model-relay /v1/internal/generations 执行生成。"""

    def __init__(self, cfg: Config, *, client: Optional[httpx.AsyncClient] = None) -> None:
        if not cfg.model_relay_url:
            raise ProviderUnavailable("AIGC_MODEL_RELAY_URL not configured")
        self._base = cfg.model_relay_url.rstrip("/")
        self._token = cfg.model_relay_internal_token
        # 整段生成(submit+poll)在 relay 内同步完成,可能数分钟;client 超时
        # 取 worker 总超时,确保后台 task 跑满预算。ops 须保证
        # BIUMIND_AIGC_TIMEOUT_S > relay 视频 poll 超时(默认 10min)。
        self._timeout = float(cfg.timeout_s)
        self._client = client or httpx.AsyncClient(timeout=self._timeout)
        self._owns_client = client is None
        self._task: Optional[asyncio.Task] = None

    @property
    def code(self) -> str:
        return "relay"

    async def aclose(self) -> None:
        if self._task is not None and not self._task.done():
            self._task.cancel()
        if self._owns_client:
            await self._client.aclose()

    async def submit(self, task: SubmitTask) -> str:
        if task.type not in ("image", "video"):
            raise ProviderError(f"relay egress only handles image/video, got {task.type!r}")
        body = self._build_body(task)
        # 后台执行同步 relay 调用,submit 立即返回 → worker 进入 running 轮询。
        self._task = asyncio.create_task(self._call(body))
        return f"relay-{task.task_id}"

    async def poll(self, external_id: str) -> Outcome:
        t = self._task
        if t is None:
            return Outcome(status="failed", error_code="INTERNAL",
                           error_message="no inflight relay task")
        if not t.done():
            return Outcome(status="running", progress=50)
        exc = t.exception()
        if exc is not None:
            if isinstance(exc, ProviderUnavailable):
                raise exc
            return Outcome(status="failed", error_code="RELAY_ERROR",
                           error_message=str(exc))
        return t.result()

    # ─── 内部 ──────────────────────────────────────────

    async def _call(self, body: dict[str, Any]) -> Outcome:
        headers = {"Content-Type": "application/json"}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        try:
            resp = await self._client.post(self._base + GENERATE_PATH,
                                           json=body, headers=headers)
        except httpx.HTTPError as e:
            raise ProviderError(f"relay http: {e}") from e

        if resp.status_code == 402:
            return Outcome(status="failed", error_code="INSUFFICIENT_CREDITS",
                           error_message=resp.text[:300])
        if resp.status_code >= 400:
            return Outcome(status="failed", error_code="RELAY_STATUS",
                           error_message=f"{resp.status_code}: {resp.text[:300]}")
        try:
            data = resp.json()
        except ValueError as e:
            raise ProviderError(f"relay decode: {e}") from e

        kind = body["type"]
        urls: list[str] = []
        metas: list[dict[str, Any]] = []
        for it in data.get("data") or []:
            u = it.get("url")
            if not u:
                continue
            urls.append(u)
            meta: dict[str, Any] = {"kind": kind}
            if it.get("cover_image_url"):
                meta["cover_url"] = it["cover_image_url"]
            if it.get("duration_ms"):
                meta["duration_ms"] = int(it["duration_ms"])
            metas.append(meta)

        if not urls:
            return Outcome(status="failed", error_code="RELAY_EMPTY",
                           error_message="relay returned no output urls")
        logger.info("relay: completed type=%s outputs=%d", kind, len(urls))
        return Outcome(status="completed", progress=100,
                       output_urls=urls, output_meta=metas)

    def _build_body(self, task: SubmitTask) -> dict[str, Any]:
        """SubmitTask + aigc params → model-relay OpenAI 兼容 body。

        idempotency_key=task_id → relay Hold 幂等,NATS 重投不双扣。
        """
        params = task.params or {}
        body: dict[str, Any] = {
            "user_id": task.user_id,
            "type": task.type,
            "idempotency_key": task.task_id,
            "model": task.model_code,
            "prompt": task.prompt,
        }
        if task.negative_prompt:
            body["negative_prompt"] = task.negative_prompt

        # size 映射(aspect_ratio+resolution → 具体尺寸)已收敛进 model-relay
        # 各 provider adaptor(dashscope 与 volcengine 尺寸表不同)。worker 只
        # 透传原始参数:显式 size 优先,否则传 aspect_ratio + resolution。
        if task.type == "image":
            n = params.get("num_outputs") or params.get("n") or 1
            body["n"] = int(n)
            for k in ("size", "aspect_ratio", "resolution"):
                if params.get(k):
                    body[k] = params[k]
            ref = params.get("reference_image_urls") or []
            if ref:
                body["reference_image_urls"] = ref
            _maybe_seed(body, params)
        else:  # video
            if params.get("duration"):
                body["duration"] = int(params["duration"])
            for k in ("resolution", "aspect_ratio", "size",
                      "first_frame_url", "last_frame_url"):
                if params.get(k):
                    body[k] = params[k]
            ref = params.get("reference_image_urls") or []
            if ref:
                body["reference_image_urls"] = ref
            _maybe_seed(body, params)
        return body


def _maybe_seed(body: dict[str, Any], params: dict[str, Any]) -> None:
    seed = params.get("seed")
    if seed not in (None, "", -1):
        try:
            body["seed"] = int(seed)
        except (TypeError, ValueError):
            pass
