"""自部署 MinerU（mineru-api）异步客户端（B1 OCR，D5 自部署 / D1 全量 PDF）。

协议（对齐 reference/llm_wiki 的 parseWithLocalMineru，仅参考思路不拷贝代码）：

    POST {api_base}/tasks               multipart/form-data 提交文件
                                        → {"task_id": "..."}
    GET  {api_base}/tasks/{task_id}     轮询 status（默认 3s 间隔）
                                        → completed / failed(error)
    GET  {api_base}/tasks/{task_id}/result
                                        → {"results": {<name>: {"md_content": ...}}}

提交参数面：lang_list=ch、parse_method=auto（D1，MinerU 内部自判扫描/文本）、
formula_enable/table_enable=true、return_md=true、return_images=false（D3 v1
丢弃图片产物）、response_format_zip=false（自托管走 JSON，不解 zip）。

错误分两类，runner 据此决定降级还是终态：

- ``OcrRetryableError``：网络错误 / 5xx / 轮询超时 —— runner 降级 pypdf 文本层。
- ``OcrTerminalError``：4xx / 任务 failed / md_content 空 —— parse_error 带
  ``[terminal]`` 前缀，brain 据此不再重扫（防反复烧 MinerU 算力）。

安全（对齐 llm_wiki）：禁重定向；状态/结果 URL 固定拼在配置的 api_base 上
（task_id quote 后拼接），不跟随响应里的绝对 URL（SSRF 防护）。
md_content 一律当纯文本处理，不进 HTML 渲染。

零新依赖：只用项目已有的 httpx + stdlib。
"""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass
from typing import Optional
from urllib.parse import quote

import httpx


logger = logging.getLogger("biumind.wiki_parse.ocr")


class OcrError(RuntimeError):
    """MinerU 调用失败基类。"""


class OcrRetryableError(OcrError):
    """可重试：网络错误 / 5xx / 轮询超时。runner 降级 pypdf，brain 可重扫。"""


class OcrTerminalError(OcrError):
    """终态：4xx / 任务 failed / md_content 空。重扫无意义，不再降级。"""


@dataclass(frozen=True)
class OcrConfig:
    api_base: str
    poll_timeout_s: float = 900.0
    poll_interval_s: float = 3.0
    submit_timeout_s: float = 300.0  # 大文件上传容忍
    request_timeout_s: float = 30.0  # 轮询 / 取结果单请求超时


def _classify(resp: httpx.Response, *, ctx: str) -> None:
    """4xx → 终态，5xx → 可重试。"""
    if 400 <= resp.status_code < 500:
        raise OcrTerminalError(f"{ctx}: HTTP {resp.status_code}: {resp.text[:300]}")
    if resp.status_code >= 500:
        raise OcrRetryableError(f"{ctx}: HTTP {resp.status_code}: {resp.text[:300]}")


async def ocr_pdf(
    cfg: OcrConfig,
    data: bytes,
    filename: str,
    *,
    transport: Optional[httpx.BaseTransport] = None,
) -> str:
    """提交 PDF 到自部署 MinerU 并轮询取回 markdown 文本。

    成功返回 md_content（非空）。失败抛 OcrRetryableError / OcrTerminalError。
    ``transport`` 是测试注入点（httpx.MockTransport），生产 None。
    """
    base = cfg.api_base.rstrip("/")
    async with httpx.AsyncClient(
        follow_redirects=False, transport=transport,
    ) as client:
        task_id = await _submit(cfg, client, base, data, filename)
        await _poll(cfg, client, f"{base}/tasks/{quote(task_id, safe='')}")
        return await _fetch_result(cfg, client, base, task_id)


async def _submit(
    cfg: OcrConfig, client: httpx.AsyncClient, base: str,
    data: bytes, filename: str,
) -> str:
    files = {"files": (filename or "document.pdf", data, "application/pdf")}
    form = {
        "lang_list": "ch",
        "parse_method": "auto",
        "formula_enable": "true",
        "table_enable": "true",
        "return_md": "true",
        "return_images": "false",
        "response_format_zip": "false",
    }
    try:
        resp = await client.post(
            f"{base}/tasks", files=files, data=form,
            timeout=cfg.submit_timeout_s,
        )
    except httpx.HTTPError as e:
        raise OcrRetryableError(f"submit failed: {e}") from e
    _classify(resp, ctx="submit")
    try:
        task_id = str(resp.json().get("task_id", "")).strip()
    except (ValueError, TypeError) as e:
        # 协议不符：按服务端异常归类（可重试，重扫上限内代价可控）
        raise OcrRetryableError(f"submit bad payload: {e}") from e
    if not task_id:
        raise OcrRetryableError("submit returned no task_id")
    return task_id


async def _poll(cfg: OcrConfig, client: httpx.AsyncClient, status_url: str) -> None:
    loop = asyncio.get_running_loop()
    deadline = loop.time() + cfg.poll_timeout_s
    while True:
        if loop.time() >= deadline:
            raise OcrRetryableError(
                f"poll timeout after {cfg.poll_timeout_s:.0f}s"
            )
        try:
            resp = await client.get(status_url, timeout=cfg.request_timeout_s)
        except httpx.HTTPError as e:
            raise OcrRetryableError(f"status check failed: {e}") from e
        _classify(resp, ctx="status")
        try:
            payload = resp.json()
        except ValueError as e:
            raise OcrRetryableError(f"status bad payload: {e}") from e
        status = str(payload.get("status", ""))
        if status == "completed":
            return
        if status == "failed":
            raise OcrTerminalError(
                f"task failed: {payload.get('error') or 'unknown error'}"
            )
        await asyncio.sleep(cfg.poll_interval_s)


async def _fetch_result(
    cfg: OcrConfig, client: httpx.AsyncClient, base: str, task_id: str,
) -> str:
    result_url = f"{base}/tasks/{quote(task_id, safe='')}/result"
    try:
        resp = await client.get(result_url, timeout=cfg.request_timeout_s)
    except httpx.HTTPError as e:
        raise OcrRetryableError(f"result download failed: {e}") from e
    _classify(resp, ctx="result")
    try:
        results = resp.json().get("results")
    except ValueError as e:
        raise OcrTerminalError(f"result bad payload: {e}") from e
    first = None
    if isinstance(results, dict) and results:
        first = next(iter(results.values()))
    md = first.get("md_content") if isinstance(first, dict) else None
    if not isinstance(md, str) or not md.strip():
        raise OcrTerminalError("MinerU returned an empty parsing result")
    return md
