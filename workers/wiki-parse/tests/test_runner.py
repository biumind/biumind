"""handle_message 的信封解包测试。

brain 的 BusPublisher 把业务 payload 包进 {topic, kind, payload} 信封
（services/brain/internal/publisher/bus.go），而 parse-queue rescan 路径
直接给业务层。本测试用哨兵异常区分「解包成功并进 handle_job」与
「没解包导致 from_payload 失败返回 None」。
"""

from __future__ import annotations

import asyncio
import json
import uuid

import pytest

import wiki_parse.runner as runner
from test_parser import _make_pdf
from wiki_parse.config import Config
from wiki_parse.job import ParseJob
from wiki_parse.ocr import OcrRetryableError, OcrTerminalError
from wiki_parse.runner import handle_message


def _cfg() -> Config:
    return Config.from_env({
        "BIUMIND_NATS_URL": "nats://test",
        "BIUMIND_ENV": "test",
        "BIUMIND_BRAIN_URL": "",
        "BIUMIND_INTERNAL_TOKEN": "",
    })


def _job_dict() -> dict:
    return {
        "source_id": str(uuid.uuid4()),
        "project_id": str(uuid.uuid4()),
        "owner_id": str(uuid.uuid4()),
        "kind": "upload",
        "mime": "application/pdf",
        "filename": "report.pdf",
    }


@pytest.mark.asyncio
async def test_envelope_wrapped_message_is_unwrapped():
    async def fetch_blob(source_id: str, owner_id: str) -> bytes:
        raise RuntimeError("sentinel: reached handle_job")

    body = json.dumps({
        "topic": "wiki.parse", "kind": "requested", "payload": _job_dict(),
    }).encode()
    with pytest.raises(RuntimeError, match="sentinel"):
        await handle_message(body, cfg=_cfg(), fetch_blob=fetch_blob)


@pytest.mark.asyncio
async def test_flat_message_still_accepted():
    async def fetch_blob(source_id: str, owner_id: str) -> bytes:
        raise RuntimeError("sentinel: reached handle_job")

    body = json.dumps(_job_dict()).encode()
    with pytest.raises(RuntimeError, match="sentinel"):
        await handle_message(body, cfg=_cfg(), fetch_blob=fetch_blob)


# ─── B1 OCR：handle_job PDF 分派 / 降级 ─────────────────────────────────


def _cfg_ocr(**extra: str) -> Config:
    env = {
        "BIUMIND_NATS_URL": "nats://test",
        "BIUMIND_ENV": "test",
        "BIUMIND_BRAIN_URL": "",
        "BIUMIND_INTERNAL_TOKEN": "",
        "BIUMIND_WIKI_PARSE_OCR_ENABLED": "true",
    }
    env.update(extra)
    return Config.from_env(env)


def _pdf_job() -> ParseJob:
    return ParseJob(
        source_id=str(uuid.uuid4()), project_id=str(uuid.uuid4()),
        owner_id=str(uuid.uuid4()), kind="upload",
        mime="application/pdf", filename="doc.pdf",
    )


class _PostCapture:
    """post_parse_result 替身：记录 kwargs 不断言网络。"""

    def __init__(self) -> None:
        self.calls: list[dict] = []

    async def __call__(self, cfg, **kw) -> None:  # noqa: ANN001
        self.calls.append(kw)


@pytest.mark.asyncio
async def test_handle_job_ocr_success_posts_parser_mineru(monkeypatch):
    async def fake_ocr(cfg, data, filename, **kw):  # noqa: ANN001
        return "# mineru markdown"
    monkeypatch.setattr(runner, "ocr_pdf", fake_ocr)
    cap = _PostCapture()
    monkeypatch.setattr(runner, "post_parse_result", cap)
    # fetch 回真 PDF 字节：count_pages 仍走 pypdf
    pdf = _make_pdf(["irrelevant text layer"])

    async def fetch_blob(source_id: str, owner_id: str) -> bytes:
        return pdf

    status, err = await runner.handle_job(
        _pdf_job(), cfg=_cfg_ocr(), fetch_blob=fetch_blob,
    )
    assert (status, err) == ("done", None)
    assert cap.calls[0]["parser"] == "mineru"
    assert cap.calls[0]["extracted_text"] == "# mineru markdown"
    assert cap.calls[0]["page_count"] == 1


@pytest.mark.asyncio
async def test_handle_job_ocr_retryable_falls_back_to_pypdf(monkeypatch):
    async def fake_ocr(cfg, data, filename, **kw):  # noqa: ANN001
        raise OcrRetryableError("mineru 503")
    monkeypatch.setattr(runner, "ocr_pdf", fake_ocr)
    cap = _PostCapture()
    monkeypatch.setattr(runner, "post_parse_result", cap)
    pdf = _make_pdf(["Hello fallback layer"])

    async def fetch_blob(source_id: str, owner_id: str) -> bytes:
        return pdf

    status, err = await runner.handle_job(
        _pdf_job(), cfg=_cfg_ocr(), fetch_blob=fetch_blob,
    )
    assert (status, err) == ("done", None)
    assert cap.calls[0]["parser"] == "pypdf"
    assert "Hello fallback layer" in cap.calls[0]["extracted_text"]


@pytest.mark.asyncio
async def test_handle_job_ocr_terminal_no_fallback(monkeypatch):
    async def fake_ocr(cfg, data, filename, **kw):  # noqa: ANN001
        raise OcrTerminalError("corrupt pdf")
    monkeypatch.setattr(runner, "ocr_pdf", fake_ocr)

    def boom_extract(*a, **k):  # noqa: ANN001
        raise AssertionError("终态不允许降级 pypdf")
    monkeypatch.setattr(runner, "extract", boom_extract)
    cap = _PostCapture()
    monkeypatch.setattr(runner, "post_parse_result", cap)

    async def fetch_blob(source_id: str, owner_id: str) -> bytes:
        return b"%PDF-1.4 whatever"

    status, err = await runner.handle_job(
        _pdf_job(), cfg=_cfg_ocr(), fetch_blob=fetch_blob,
    )
    assert status == "error"
    assert err.startswith("[terminal]")
    # handle_job 自身不回写 error（由 handle_message / tick 路径补写）
    assert cap.calls == []


@pytest.mark.asyncio
async def test_handle_job_ocr_disabled_uses_pypdf(monkeypatch):
    async def boom_ocr(*a, **k):  # noqa: ANN001
        raise AssertionError("OCR 未启用不应调 MinerU")
    monkeypatch.setattr(runner, "ocr_pdf", boom_ocr)
    cap = _PostCapture()
    monkeypatch.setattr(runner, "post_parse_result", cap)
    pdf = _make_pdf(["Plain pypdf path"])

    async def fetch_blob(source_id: str, owner_id: str) -> bytes:
        return pdf

    cfg = Config.from_env({
        "BIUMIND_NATS_URL": "nats://test",
        "BIUMIND_ENV": "test",
        "BIUMIND_BRAIN_URL": "",
        "BIUMIND_INTERNAL_TOKEN": "",
    })
    status, err = await runner.handle_job(
        _pdf_job(), cfg=cfg, fetch_blob=fetch_blob,
    )
    assert (status, err) == ("done", None)
    assert cap.calls[0]["parser"] == "pypdf"
    assert "Plain pypdf path" in cap.calls[0]["extracted_text"]


# ─── JobDispatcher：Semaphore 并发上限 + 崩溃隔离 + error 回写 ────────────


@pytest.mark.asyncio
async def test_dispatcher_respects_max_concurrency_and_no_task_lost(monkeypatch):
    cfg = _cfg_ocr(BIUMIND_WIKI_PARSE_MAX_CONCURRENCY="2")
    running = 0
    peak = 0
    done = 0

    async def fake_handle_message(raw, *, cfg, fetch_blob=None):  # noqa: ANN001
        nonlocal running, peak, done
        running += 1
        peak = max(peak, running)
        await asyncio.sleep(0.05)
        running -= 1
        done += 1

    monkeypatch.setattr(runner, "handle_message", fake_handle_message)
    d = runner.JobDispatcher(cfg)
    for _ in range(6):
        d.dispatch_message(b"{}")
    await d.wait_all()
    assert done == 6            # 不丢任务
    assert peak <= 2            # 并发上限生效


@pytest.mark.asyncio
async def test_dispatcher_crash_does_not_kill_others(monkeypatch):
    cfg = _cfg_ocr()
    done = 0

    async def fake_handle_message(raw, *, cfg, fetch_blob=None):  # noqa: ANN001
        nonlocal done
        if raw == b"boom":
            raise RuntimeError("handler crashed")
        await asyncio.sleep(0.01)
        done += 1

    monkeypatch.setattr(runner, "handle_message", fake_handle_message)
    d = runner.JobDispatcher(cfg)
    d.dispatch_message(b"boom")
    for _ in range(3):
        d.dispatch_message(b"ok")
    await d.wait_all()
    assert done == 3


@pytest.mark.asyncio
async def test_dispatcher_job_error_posts_back(monkeypatch):
    async def fake_handle_job(job, *, cfg, fetch_blob=None):  # noqa: ANN001
        return "error", "boom"
    monkeypatch.setattr(runner, "handle_job", fake_handle_job)
    cap = _PostCapture()
    monkeypatch.setattr(runner, "post_parse_result", cap)

    d = runner.JobDispatcher(_cfg_ocr())
    job = _pdf_job()
    d.dispatch_job(job)
    await d.wait_all()
    assert cap.calls[0]["parse_status"] == "error"
    assert cap.calls[0]["parse_error"] == "boom"
    assert cap.calls[0]["source_id"] == job.source_id


def test_config_ocr_defaults():
    cfg = Config.from_env({})
    assert cfg.ocr_enabled is False
    assert cfg.mineru_api_base == "http://mineru:8000"
    assert cfg.ocr_poll_timeout_s == 900
    assert cfg.max_concurrency == 4
