"""HotparseProvider + prompt 解析测试 (mock relay STT/chat + 下载/抽音轨)。"""

from __future__ import annotations

import asyncio
import json
import os
import tempfile

import httpx
import pytest
import respx

from biumind_aigc.config import Config
from biumind_aigc.event import SubmitTask
from biumind_aigc.hotparse import download as dl
from biumind_aigc.hotparse import extractors as ext
from biumind_aigc.hotparse import prompt as hp
from biumind_aigc.providers import get as get_provider
from biumind_aigc.providers.base import ProviderError, ProviderUnavailable
from biumind_aigc.providers.hotparse_provider import (
    CHAT_PATH,
    TRANSCRIBE_PATH,
    HotparseProvider,
)


RELAY = "http://model-relay:7001"


def _cfg(**over) -> Config:
    env = {
        "NATS_URL": "x",
        "AIGC_MODEL_RELAY_URL": RELAY,
        "IDENTITY_INTERNAL_TOKEN": "tok",
        "BIUMIND_AIGC_TIMEOUT_S": "30",
    }
    env.update(over)
    return Config.from_env(env)


def _task(**over) -> SubmitTask:
    base = {
        "task_id": "tid", "user_id": "uid", "type": "hotparse",
        "model_code": "hotparse-v1", "provider_code": "hotparse",
        "prompt": "", "cost_credits": 10,
        "params": {"source_url": "https://x/v.mp4"},
    }
    base.update(over)
    return SubmitTask(**base)


# ─── 路由 / 配置 ─────────────────────────────────

def test_get_routes_to_hotparse() -> None:
    assert isinstance(get_provider("hotparse", _cfg()), HotparseProvider)


def test_no_relay_url_raises_unavailable() -> None:
    with pytest.raises(ProviderUnavailable):
        HotparseProvider(_cfg(AIGC_MODEL_RELAY_URL=""))


@pytest.mark.asyncio
async def test_submit_requires_source_url() -> None:
    p = HotparseProvider(_cfg(), client=httpx.AsyncClient())
    with pytest.raises(ProviderError):
        await p.submit(_task(params={}))


# ─── prompt 解析 ─────────────────────────────────

def test_parse_result_strips_fence() -> None:
    body = {"copywriting": "c", "hooks": ["h"],
            "scenes": [{"index": 1, "prompt": "p"}], "tags": ["t"]}
    txt = "```json\n" + json.dumps(body) + "\n```"
    r = hp.parse_result(txt)
    assert r["copywriting"] == "c"
    assert r["scenes"][0]["prompt"] == "p"
    assert r["tags"] == ["t"]


def test_parse_result_drops_scene_without_prompt() -> None:
    r = hp.parse_result('{"copywriting":"c","scenes":[{"index":1,"description":"d"}]}')
    assert r["scenes"] == []


def test_parse_result_empty_raises() -> None:
    with pytest.raises(ValueError):
        hp.parse_result("{}")


def test_parse_result_invalid_json_raises() -> None:
    with pytest.raises(ValueError):
        hp.parse_result("not json at all")


# ─── 全链路 (mock 下载 + relay) ──────────────────

@pytest.mark.asyncio
@respx.mock
async def test_full_pipeline(monkeypatch) -> None:
    async def fake_dl(url, *, client):
        return "/tmp/does-not-exist.mp4"  # finally 里 isfile=False, 不 unlink

    async def fake_extract(path):
        fd, p = tempfile.mkstemp(suffix=".m4a")
        os.write(fd, b"fake-audio-bytes")
        os.close(fd)
        return p

    monkeypatch.setattr(dl, "download_to_tmp", fake_dl)
    monkeypatch.setattr(dl, "extract_audio", fake_extract)

    respx.post(RELAY + TRANSCRIBE_PATH).mock(return_value=httpx.Response(
        200, json={"text": "转写文本内容", "language": "zh"}))
    analysis = {
        "copywriting": "改写文案", "hooks": ["前3秒钩子"],
        "scenes": [{"index": 1, "description": "开场", "prompt": "成品提示词", "duration_hint_s": 3}],
        "tags": ["标签A"],
    }
    respx.post(RELAY + CHAT_PATH).mock(return_value=httpx.Response(
        200, json={"content": [{"type": "text", "text": json.dumps(analysis)}],
                   "usage": {"input_tokens": 10, "output_tokens": 20}}))

    p = HotparseProvider(_cfg(), client=httpx.AsyncClient())
    ext = await p.submit(_task())
    assert ext.startswith("hotparse-")
    out = None
    for _ in range(200):
        out = await p.poll(ext)
        if out.status != "running":
            break
        await asyncio.sleep(0.01)
    assert out is not None and out.status == "completed", out
    assert out.structured["copywriting"] == "改写文案"
    assert out.structured["transcript"] == "转写文本内容"
    assert out.structured["scenes"][0]["prompt"] == "成品提示词"
    assert out.structured["tags"] == ["标签A"]


# ─── Phase2 平台提取 ─────────────────────────

def test_detect_source() -> None:
    assert ext.detect_source("https://www.bilibili.com/video/BV1xx") == "bilibili"
    assert ext.detect_source("https://b23.tv/abc") == "bilibili"
    assert ext.detect_source("https://v.douyin.com/abc/") == "douyin"
    assert ext.detect_source("https://www.xiaohongshu.com/x") == "xiaohongshu"
    assert ext.detect_source("https://cdn.example.com/v.mp4") == "upload"


@pytest.mark.asyncio
async def test_xiaohongshu_deferred() -> None:
    p = HotparseProvider(_cfg(), client=httpx.AsyncClient())
    ext_id = await p.submit(_task(params={"source_url": "https://www.xiaohongshu.com/x"}))
    out = None
    for _ in range(200):
        out = await p.poll(ext_id)
        if out.status != "running":
            break
        await asyncio.sleep(0.01)
    assert out is not None and out.status == "failed"
    assert out.error_code == "HOTPARSE_SOURCE_UNSUPPORTED"


@pytest.mark.asyncio
@respx.mock
async def test_platform_routes_to_ytdlp(monkeypatch) -> None:
    called = {}

    async def fake_ytdlp(url, *, max_bytes=0):
        called["url"] = url
        fd, p = tempfile.mkstemp(suffix=".m4a")
        os.write(fd, b"audio")
        os.close(fd)
        return p, {"cover_url": "", "duration_ms": 1000, "title": "t"}

    async def fake_extract(path):
        fd, p = tempfile.mkstemp(suffix=".m4a")
        os.write(fd, b"x")
        os.close(fd)
        return p

    monkeypatch.setattr(ext, "ytdlp_download", fake_ytdlp)
    monkeypatch.setattr(dl, "extract_audio", fake_extract)
    respx.post(RELAY + TRANSCRIBE_PATH).mock(return_value=httpx.Response(
        200, json={"text": "B站转写"}))
    analysis = {"copywriting": "c", "scenes": [{"index": 1, "prompt": "p"}], "tags": [], "hooks": []}
    respx.post(RELAY + CHAT_PATH).mock(return_value=httpx.Response(
        200, json={"content": [{"type": "text", "text": json.dumps(analysis)}]}))

    p = HotparseProvider(_cfg(), client=httpx.AsyncClient())
    ext_id = await p.submit(_task(params={"source_url": "https://www.bilibili.com/video/BV1xx"}))
    out = None
    for _ in range(200):
        out = await p.poll(ext_id)
        if out.status != "running":
            break
        await asyncio.sleep(0.01)
    assert out is not None and out.status == "completed", out
    assert called.get("url") == "https://www.bilibili.com/video/BV1xx"  # 走了 yt-dlp
    assert out.structured["transcript"] == "B站转写"


@pytest.mark.asyncio
@respx.mock
async def test_transcribe_error_maps_failed(monkeypatch) -> None:
    async def fake_dl(url, *, client):
        return "/tmp/does-not-exist.mp4"

    async def fake_extract(path):
        fd, p = tempfile.mkstemp(suffix=".m4a")
        os.write(fd, b"x")
        os.close(fd)
        return p

    monkeypatch.setattr(dl, "download_to_tmp", fake_dl)
    monkeypatch.setattr(dl, "extract_audio", fake_extract)
    respx.post(RELAY + TRANSCRIBE_PATH).mock(return_value=httpx.Response(
        500, text="upstream boom"))

    p = HotparseProvider(_cfg(), client=httpx.AsyncClient())
    ext = await p.submit(_task())
    out = None
    for _ in range(200):
        out = await p.poll(ext)
        if out.status != "running":
            break
        await asyncio.sleep(0.01)
    assert out is not None and out.status == "failed"
    assert out.error_code == "HOTPARSE_ERROR"
