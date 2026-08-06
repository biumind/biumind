"""HotparseProvider — 爆款解析执行器.

链路: 短视频直链 → 下载 → ffmpeg 抽音轨 → model-relay STT 转写 →
model-relay LLM 拆解 → 结构化结果 {文案/钩子/分镜/标签}。

按 I6,STT/LLM 不直 import SDK,经 model-relay 内部端点:
  POST {relay}/v1/internal/transcribe   (multipart, X-Internal-User-Id 头)
  POST {relay}/v1/internal/chat         (Anthropic 兼容 JSON, body 带 user_id)

沿用 RelayProvider 的「submit 起后台 task / poll 查状态」模式(整条链路是多步
阻塞调用,submit 立即返回让 worker 主循环能发 running 进度)。

产物是结构化 JSON(非媒体 URL),放 Outcome.structured —— worker completed
分支识别后构造一条 kind="hotparse" 的 OutputEntry(metadata=结果),跳过 persist。
"""

from __future__ import annotations

import asyncio
import logging
import os
from typing import Any, Optional

import httpx

from ..config import Config
from ..event import SubmitTask
from ..hotparse import download as _dl
from ..hotparse import prompt as _prompt
from ..hotparse import extractors as _ext
from .base import Executor, Outcome, ProviderError, ProviderUnavailable


logger = logging.getLogger("biumind.aigc.hotparse")

TRANSCRIBE_PATH = "/v1/internal/transcribe"
CHAT_PATH = "/v1/internal/chat"


class HotparseProvider(Executor):
    """经 model-relay 内部端点执行 STT + LLM 拆解。"""

    def __init__(self, cfg: Config, *, client: Optional[httpx.AsyncClient] = None) -> None:
        if not cfg.model_relay_url:
            raise ProviderUnavailable("AIGC_MODEL_RELAY_URL not configured")
        self._cfg = cfg
        self._base = cfg.model_relay_url.rstrip("/")
        self._token = cfg.model_relay_internal_token
        self._timeout = float(cfg.timeout_s)
        self._client = client or httpx.AsyncClient(timeout=self._timeout)
        self._owns_client = client is None
        self._task: Optional[asyncio.Task] = None
        self._progress = 0

    @property
    def code(self) -> str:
        return "hotparse"

    async def aclose(self) -> None:
        if self._task is not None and not self._task.done():
            self._task.cancel()
        if self._owns_client:
            await self._client.aclose()

    async def submit(self, task: SubmitTask) -> str:
        if task.type != "hotparse":
            raise ProviderError(f"hotparse provider got type={task.type!r}")
        source_url = (task.params or {}).get("source_url") or (task.params or {}).get("source_video_url")
        if not source_url:
            raise ProviderError("hotparse: params.source_url required")
        self._task = asyncio.create_task(self._run(task, str(source_url)))
        return f"hotparse-{task.task_id}"

    async def poll(self, external_id: str) -> Outcome:
        t = self._task
        if t is None:
            return Outcome(status="failed", error_code="INTERNAL",
                           error_message="no inflight hotparse task")
        if not t.done():
            return Outcome(status="running", progress=max(1, self._progress))
        exc = t.exception()
        if exc is not None:
            if isinstance(exc, ProviderUnavailable):
                raise exc
            return Outcome(status="failed", error_code="HOTPARSE_ERROR",
                           error_message=str(exc))
        return t.result()

    # ─── 内部 ──────────────────────────────────────────

    async def _run(self, task: SubmitTask, source_url: str) -> Outcome:
        video_path: Optional[str] = None
        audio_path: Optional[str] = None
        is_ytdlp = False
        # source: 客户端显式给 (params.source), 缺省按 URL 推断。
        source = (task.params or {}).get("source") or _ext.detect_source(source_url)
        try:
            # 1) 取媒体: upload=公网直链流式下载; douyin/bilibili=yt-dlp; 其余暂缓。
            self._progress = 15
            if source == "upload":
                video_path = await _dl.download_to_tmp(source_url, client=self._client)
            elif source in _ext.PLATFORM_SOURCES:
                video_path, _meta = await _ext.ytdlp_download(source_url)
                is_ytdlp = True
            elif source in _ext.DEFERRED_SOURCES:
                return Outcome(status="failed", error_code="HOTPARSE_SOURCE_UNSUPPORTED",
                               error_message=f"{source} 解析即将上线,请改用公网视频直链")
            else:
                return Outcome(status="failed", error_code="HOTPARSE_SOURCE_UNSUPPORTED",
                               error_message=f"暂不支持的来源: {source}")
            # 2) 抽音轨
            self._progress = 35
            audio_path = await _dl.extract_audio(video_path)
            # 3) STT 转写
            self._progress = 55
            transcript = await self._transcribe(task, audio_path)
            # 4) LLM 拆解
            self._progress = 80
            structured = await self._analyze(task, transcript)
            structured["transcript"] = transcript
            self._progress = 100
            logger.info("hotparse: completed task_id=%s scenes=%d",
                        task.task_id, len(structured.get("scenes") or []))
            return Outcome(status="completed", progress=100, structured=structured)
        except _ext.ExtractError as e:
            return Outcome(status="failed", error_code="HOTPARSE_EXTRACT",
                           error_message=str(e))
        except _dl.DownloadError as e:
            return Outcome(status="failed", error_code="HOTPARSE_DOWNLOAD",
                           error_message=str(e))
        except ValueError as e:
            # prompt.parse_result 抛 —— LLM 输出不合法
            return Outcome(status="failed", error_code="HOTPARSE_PARSE",
                           error_message=str(e))
        finally:
            if video_path:
                if is_ytdlp:
                    _ext.cleanup(video_path)  # 连同临时目录
                elif os.path.isfile(video_path):
                    try:
                        os.unlink(video_path)
                    except OSError:
                        pass
            if audio_path and os.path.isfile(audio_path):
                try:
                    os.unlink(audio_path)
                except OSError:
                    pass

    async def _transcribe(self, task: SubmitTask, audio_path: str) -> str:
        model = (task.params or {}).get("stt_model") or self._cfg.hotparse_stt_model
        headers = {"X-Internal-User-Id": task.user_id, "X-Request-Id": task.task_id}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        with open(audio_path, "rb") as f:
            files = {"file": ("audio.m4a", f, "audio/m4a")}
            data = {"model": model}
            try:
                resp = await self._client.post(
                    self._base + TRANSCRIBE_PATH,
                    headers=headers, files=files, data=data,
                )
            except httpx.HTTPError as e:
                raise ProviderError(f"transcribe http: {e}") from e
        if resp.status_code >= 400:
            raise ProviderError(f"transcribe {resp.status_code}: {resp.text[:300]}")
        try:
            body = resp.json()
        except ValueError as e:
            raise ProviderError(f"transcribe decode: {e}") from e
        text = (body.get("text") or "").strip()
        if not text:
            raise ProviderError("transcribe returned empty text")
        return text

    async def _analyze(self, task: SubmitTask, transcript: str) -> dict[str, Any]:
        model = (task.params or {}).get("llm_model") or self._cfg.hotparse_llm_model
        headers = {"Content-Type": "application/json"}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        body = {
            "user_id": task.user_id,
            "idempotency_key": task.task_id,
            "model": model,
            "max_tokens": 2048,
            "stream": False,
            "system": _prompt.SYSTEM_PROMPT,
            "messages": _prompt.build_messages(transcript),
        }
        try:
            resp = await self._client.post(
                self._base + CHAT_PATH, headers=headers, json=body,
            )
        except httpx.HTTPError as e:
            raise ProviderError(f"chat http: {e}") from e
        if resp.status_code >= 400:
            raise ProviderError(f"chat {resp.status_code}: {resp.text[:300]}")
        try:
            data = resp.json()
        except ValueError as e:
            raise ProviderError(f"chat decode: {e}") from e
        text = _extract_anthropic_text(data)
        if not text:
            raise ProviderError("chat returned empty content")
        # parse_result 抛 ValueError → _run 转 HOTPARSE_PARSE
        return _prompt.parse_result(text)


def _extract_anthropic_text(data: dict[str, Any]) -> str:
    """从 Anthropic /v1/messages 响应里拼接所有 text block。"""
    parts: list[str] = []
    for block in data.get("content") or []:
        if isinstance(block, dict) and block.get("type") == "text":
            parts.append(str(block.get("text") or ""))
    return "".join(parts).strip()
