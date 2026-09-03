"""MinerU OCR 客户端单测（httpx.MockTransport，不起真服务）。

三态覆盖：
- 成功：submit → poll completed → result 取 results[*].md_content
- 可重试（OcrRetryableError）：网络错误 / submit 5xx / 轮询超时 / result 5xx
- 终态（OcrTerminalError）：submit 4xx / task failed / md_content 空

另校验 submit multipart 字段面（lang_list=ch、parse_method=auto 等，对齐
reference/llm_wiki parseWithLocalMineru 契约）。
"""

from __future__ import annotations

import httpx
import pytest

from wiki_parse.ocr import (
    OcrConfig, OcrRetryableError, OcrTerminalError, ocr_pdf,
)


def _cfg(**kw) -> OcrConfig:
    base = dict(
        api_base="http://mineru:8000",
        poll_interval_s=0.01,
        poll_timeout_s=5.0,
    )
    base.update(kw)
    return OcrConfig(**base)


def _handler_ok(md: str = "# OCR 文本", captured: dict | None = None):
    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "POST" and request.url.path == "/tasks":
            if captured is not None:
                captured["submit_body"] = request.content
            return httpx.Response(200, json={"task_id": "t-1"})
        if request.url.path == "/tasks/t-1":
            return httpx.Response(200, json={"status": "completed"})
        if request.url.path == "/tasks/t-1/result":
            return httpx.Response(
                200, json={"results": {"doc.pdf": {"md_content": md}}},
            )
        return httpx.Response(404, json={"error": "unexpected"})
    return handler


@pytest.mark.asyncio
async def test_ocr_success_returns_md_content():
    captured: dict = {}
    transport = httpx.MockTransport(_handler_ok(captured=captured))
    out = await ocr_pdf(_cfg(), b"%PDF-1.4 fake", "doc.pdf", transport=transport)
    assert out == "# OCR 文本"
    # submit multipart 字段面（契约形状，对齐 llm_wiki）
    body = captured["submit_body"]
    for field in (
        b'name="files"', b'lang_list', b"ch",
        b"parse_method", b"auto",
        b"formula_enable", b"table_enable",
        b"return_md", b"return_images",
    ):
        assert field in body, field


@pytest.mark.asyncio
async def test_ocr_submit_4xx_is_terminal():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(400, json={"error": "unsupported file"})
    with pytest.raises(OcrTerminalError, match="HTTP 400"):
        await ocr_pdf(
            _cfg(), b"bad", "x.pdf", transport=httpx.MockTransport(handler),
        )


@pytest.mark.asyncio
async def test_ocr_submit_5xx_is_retryable():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, text="mineru overloaded")
    with pytest.raises(OcrRetryableError, match="HTTP 503"):
        await ocr_pdf(
            _cfg(), b"data", "x.pdf", transport=httpx.MockTransport(handler),
        )


@pytest.mark.asyncio
async def test_ocr_network_error_is_retryable():
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused", request=request)
    with pytest.raises(OcrRetryableError, match="connection refused"):
        await ocr_pdf(
            _cfg(), b"data", "x.pdf", transport=httpx.MockTransport(handler),
        )


@pytest.mark.asyncio
async def test_ocr_task_failed_is_terminal():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "POST":
            return httpx.Response(200, json={"task_id": "t-1"})
        if request.url.path == "/tasks/t-1":
            return httpx.Response(
                200, json={"status": "failed", "error": "corrupt pdf"},
            )
        return httpx.Response(404)
    with pytest.raises(OcrTerminalError, match="corrupt pdf"):
        await ocr_pdf(
            _cfg(), b"data", "x.pdf", transport=httpx.MockTransport(handler),
        )


@pytest.mark.asyncio
async def test_ocr_empty_md_content_is_terminal():
    transport = httpx.MockTransport(_handler_ok(md="   "))
    with pytest.raises(OcrTerminalError, match="empty"):
        await ocr_pdf(_cfg(), b"data", "x.pdf", transport=transport)


@pytest.mark.asyncio
async def test_ocr_poll_timeout_is_retryable():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "POST":
            return httpx.Response(200, json={"task_id": "t-1"})
        return httpx.Response(200, json={"status": "running"})
    cfg = _cfg(poll_timeout_s=0.05)
    with pytest.raises(OcrRetryableError, match="timeout"):
        await ocr_pdf(cfg, b"data", "x.pdf", transport=httpx.MockTransport(handler))


@pytest.mark.asyncio
async def test_ocr_result_5xx_is_retryable():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "POST":
            return httpx.Response(200, json={"task_id": "t-1"})
        if request.url.path == "/tasks/t-1":
            return httpx.Response(200, json={"status": "completed"})
        return httpx.Response(500, text="boom")
    with pytest.raises(OcrRetryableError, match="HTTP 500"):
        await ocr_pdf(
            _cfg(), b"data", "x.pdf", transport=httpx.MockTransport(handler),
        )
