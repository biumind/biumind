"""HTTP client for brain's Phase 3 internal endpoints.

三个端点合成 parser 闭环（均需 X-Biumind-Internal-Token + owner_id 配对，
除 parse-queue 跨 owner 返最小元数据）：

    GET  /v1/internal/wiki/sources/parse-queue          rescan 拉 queued/error 行
    GET  /v1/internal/wiki/sources/{id}/blob-presign    取文件 presigned GET URL
    POST /v1/internal/wiki/sources/{id}/parse-result    回写 extracted_text/hash/status

worker 不直连 MinIO —— presigned URL 由 brain 用 blob.PresignGet 签发，
worker httpx 下载，永远不见 MinIO 凭据（4 worker 形态一致：只 NATS + brain HTTP）。
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import List, Optional

import httpx


logger = logging.getLogger("biumind.wiki_parse.brain")


class BrainClientError(RuntimeError):
    """transport / status / payload 错误。"""


@dataclass(frozen=True)
class BrainConfig:
    base_url: str
    internal_token: str
    timeout_s: float = 30.0


def _require(cfg: BrainConfig) -> None:
    if not cfg.base_url:
        raise BrainClientError("brain base_url missing — set BIUMIND_BRAIN_URL")
    if not cfg.internal_token:
        raise BrainClientError(
            "brain internal token missing — "
            "set BIUMIND_INTERNAL_TOKEN to match the brain env"
        )


def _client(cfg: BrainConfig) -> httpx.AsyncClient:
    timeout = httpx.Timeout(connect=5.0, read=cfg.timeout_s, write=5.0, pool=5.0)
    return httpx.AsyncClient(timeout=timeout)


def _check(resp: httpx.Response, *, ctx: str) -> None:
    if resp.status_code >= 400:
        raise BrainClientError(
            f"{ctx}: HTTP {resp.status_code}: {resp.text[:300]}"
        )


@dataclass(frozen=True)
class BlobPresign:
    url: str
    filename: str
    mime: str

    @classmethod
    def from_payload(cls, p: dict) -> "BlobPresign":
        return cls(
            url=str(p.get("url", "")),
            filename=str(p.get("filename", "") or ""),
            mime=str(p.get("mime", "") or ""),
        )


@dataclass(frozen=True)
class QueueItem:
    source_id: str
    project_id: str
    owner_id: str
    kind: str
    mime: str
    filename: str

    @classmethod
    def from_payload(cls, p: dict) -> "QueueItem":
        return cls(
            source_id=str(p.get("source_id", "")),
            project_id=str(p.get("project_id", "")),
            owner_id=str(p.get("owner_id", "")),
            kind=str(p.get("kind", "upload") or "upload"),
            mime=str(p.get("mime", "") or ""),
            filename=str(p.get("filename", "") or ""),
        )

    def as_job(self):  # -> ParseJob（避免循环 import，局部导入）
        from .job import ParseJob
        return ParseJob(
            source_id=self.source_id, project_id=self.project_id,
            owner_id=self.owner_id, kind=self.kind,
            mime=self.mime, filename=self.filename,
        )


async def get_blob_presign(
    cfg: BrainConfig, *, source_id: str, owner_id: str,
) -> Optional[BlobPresign]:
    _require(cfg)
    url = (
        cfg.base_url.rstrip("/")
        + f"/v1/internal/wiki/sources/{source_id}/blob-presign"
    )
    headers = {"X-Biumind-Internal-Token": cfg.internal_token}
    params = {"owner_id": owner_id}
    async with _client(cfg) as c:
        try:
            resp = await c.get(url, headers=headers, params=params)
        except httpx.HTTPError as e:
            raise BrainClientError(f"blob-presign failed: {e}") from e
    if resp.status_code == 404:
        return None
    _check(resp, ctx="blob-presign")
    try:
        return BlobPresign.from_payload(resp.json())
    except (ValueError, TypeError) as e:
        raise BrainClientError(f"bad blob-presign payload: {e}") from e


async def download_blob(
    presign_url: str, *, max_bytes: int = 200 * 1024 * 1024,
) -> bytes:
    """Stream-download presigned URL，cap max_bytes（zip-bomb 防护）。"""
    body = bytearray()
    timeout = httpx.Timeout(connect=10.0, read=120.0, write=10.0, pool=10.0)
    async with httpx.AsyncClient(timeout=timeout, follow_redirects=True) as c:
        try:
            async with c.stream("GET", presign_url) as resp:
                if resp.status_code >= 400:
                    # 流式响应未 read() 前不能碰 resp.text（ResponseNotRead）。
                    # 读少量错误体用于诊断，读不动就算了 —— 状态码已是主要信息。
                    try:
                        err_body = (await resp.aread())[:200]
                    except httpx.HTTPError:
                        err_body = b""
                    raise BrainClientError(
                        f"download HTTP {resp.status_code}: {err_body!r}"
                    )
                async for chunk in resp.aiter_bytes(chunk_size=64 * 1024):
                    body.extend(chunk)
                    if len(body) > max_bytes:
                        raise BrainClientError(
                            f"file exceeds {max_bytes} bytes limit"
                        )
        except httpx.HTTPError as e:
            raise BrainClientError(f"download failed: {e}") from e
    return bytes(body)


async def post_parse_result(
    cfg: BrainConfig, *, source_id: str, owner_id: str,
    extracted_text: str, content_hash: str, parse_status: str,
    parse_error: str = "", page_count: Optional[int] = None,
) -> None:
    _require(cfg)
    url = (
        cfg.base_url.rstrip("/")
        + f"/v1/internal/wiki/sources/{source_id}/parse-result"
    )
    headers = {"X-Biumind-Internal-Token": cfg.internal_token}
    params = {"owner_id": owner_id}
    payload = {
        "extracted_text": extracted_text,
        "content_hash": content_hash,
        "parse_status": parse_status,
        "parse_error": parse_error,
    }
    if page_count is not None:
        payload["page_count"] = page_count
    async with _client(cfg) as c:
        try:
            resp = await c.post(url, headers=headers, params=params, json=payload)
        except httpx.HTTPError as e:
            raise BrainClientError(f"parse-result failed: {e}") from e
    _check(resp, ctx="parse-result")


async def get_parse_queue(cfg: BrainConfig) -> List[QueueItem]:
    _require(cfg)
    url = cfg.base_url.rstrip("/") + "/v1/internal/wiki/sources/parse-queue"
    headers = {"X-Biumind-Internal-Token": cfg.internal_token}
    async with _client(cfg) as c:
        try:
            resp = await c.get(url, headers=headers)
        except httpx.HTTPError as e:
            raise BrainClientError(f"parse-queue failed: {e}") from e
    _check(resp, ctx="parse-queue")
    try:
        items = resp.json().get("sources", [])
        return [QueueItem.from_payload(it) for it in items]
    except (ValueError, TypeError) as e:
        raise BrainClientError(f"bad parse-queue payload: {e}") from e
