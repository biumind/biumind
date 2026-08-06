"""RelayProvider 测试 — 段3.6 生成经 model-relay 单一 egress."""

from __future__ import annotations

import asyncio

import httpx
import pytest
import respx

from biumind_aigc.config import Config
from biumind_aigc.event import SubmitTask
from biumind_aigc.providers import get as get_provider
from biumind_aigc.providers.base import ProviderUnavailable
from biumind_aigc.providers.relay_provider import (
    GENERATE_PATH,
    RelayProvider,
)


RELAY = "http://model-relay:7001"


def _cfg(**over) -> Config:
    env = {
        "NATS_URL": "x",
        "AIGC_GENERATE_VIA_RELAY": "true",
        "AIGC_MODEL_RELAY_URL": RELAY,
        "IDENTITY_INTERNAL_TOKEN": "tok",
        "BIUMIND_AIGC_TIMEOUT_S": "30",
    }
    env.update(over)
    return Config.from_env(env)


def _task(**over) -> SubmitTask:
    base = {
        "task_id": "tid", "user_id": "uid", "type": "image",
        "model_code": "wanx-2.6-t2i", "provider_code": "dashscope",
        "prompt": "柯基", "cost_credits": 30,
        "params": {"aspect_ratio": "16:9", "resolution": "1080p", "n": 2, "seed": 7},
    }
    base.update(over)
    return SubmitTask(**base)


def test_no_relay_url_raises_unavailable() -> None:
    with pytest.raises(ProviderUnavailable):
        RelayProvider(_cfg(AIGC_MODEL_RELAY_URL=""))


def test_get_routes_to_relay_when_flag_on() -> None:
    p = get_provider("dashscope", _cfg())
    assert isinstance(p, RelayProvider)


def test_flag_off_dashscope_raises_after_direct_removed() -> None:
    # 段3.6 后直连 provider 已删:flag off + dashscope → 无 REGISTRY 条目,
    # 抛 ProviderUnavailable(提示开 flag),不再有直连回落。
    import pytest
    from biumind_aigc.providers.base import ProviderUnavailable
    with pytest.raises(ProviderUnavailable):
        get_provider("dashscope", _cfg(AIGC_GENERATE_VIA_RELAY="false"))


def test_get_falls_back_when_provider_not_in_allowlist() -> None:
    # stub 不在 relay 名单 → 即便 flag on 也走 REGISTRY(StubProvider)
    p = get_provider("stub", _cfg())
    assert not isinstance(p, RelayProvider)


def test_get_routes_volcengine_to_relay() -> None:
    # volcengine 已纳入 relay 名单 (Phase 2)
    p = get_provider("volcengine", _cfg())
    assert isinstance(p, RelayProvider)


def test_build_body_image() -> None:
    rp = RelayProvider(_cfg(), client=httpx.AsyncClient())
    body = rp._build_body(_task(negative_prompt="模糊"))
    assert body["user_id"] == "uid"
    assert body["type"] == "image"
    assert body["idempotency_key"] == "tid"  # = task_id, Hold 幂等
    assert body["model"] == "wanx-2.6-t2i"
    assert body["negative_prompt"] == "模糊"
    assert body["n"] == 2
    # size 映射收敛进 model-relay adaptor;worker 只透传原始参数。
    assert body["aspect_ratio"] == "16:9"
    assert body["resolution"] == "1080p"
    assert "size" not in body  # 未显式给 size
    assert body["seed"] == 7


def test_build_body_video() -> None:
    rp = RelayProvider(_cfg(), client=httpx.AsyncClient())
    body = rp._build_body(_task(
        type="video", model_code="wanx-2.6-t2v",
        params={"duration": 5, "resolution": "1080P", "first_frame_url": "http://x/a.png"},
    ))
    assert body["type"] == "video"
    assert body["duration"] == 5
    assert body["resolution"] == "1080P"
    assert body["first_frame_url"] == "http://x/a.png"


@pytest.mark.asyncio
@respx.mock
async def test_submit_poll_happy_path() -> None:
    respx.post(RELAY + GENERATE_PATH).mock(return_value=httpx.Response(
        200, json={"created": 1, "data": [{"url": "https://oss/img.png"}]}))

    rp = RelayProvider(_cfg(), client=httpx.AsyncClient())
    ext = await rp.submit(_task())
    assert ext.startswith("relay-")
    # 等后台 task 完成
    for _ in range(50):
        out = await rp.poll(ext)
        if out.status != "running":
            break
        await asyncio.sleep(0.01)
    assert out.status == "completed"
    assert out.output_urls == ["https://oss/img.png"]
    assert out.output_meta[0]["kind"] == "image"


@pytest.mark.asyncio
@respx.mock
async def test_video_cover_and_duration_mapped() -> None:
    respx.post(RELAY + GENERATE_PATH).mock(return_value=httpx.Response(
        200, json={"data": [{"url": "https://oss/v.mp4",
                             "cover_image_url": "https://oss/c.png",
                             "duration_ms": 5000}]}))
    rp = RelayProvider(_cfg(), client=httpx.AsyncClient())
    ext = await rp.submit(_task(type="video", model_code="wanx-2.6-t2v",
                                params={"duration": 5}))
    for _ in range(50):
        out = await rp.poll(ext)
        if out.status != "running":
            break
        await asyncio.sleep(0.01)
    assert out.status == "completed"
    assert out.output_meta[0]["cover_url"] == "https://oss/c.png"
    assert out.output_meta[0]["duration_ms"] == 5000


@pytest.mark.asyncio
@respx.mock
async def test_insufficient_credits_maps_failed() -> None:
    respx.post(RELAY + GENERATE_PATH).mock(return_value=httpx.Response(
        402, text='{"error":{"code":"insufficient_credits"}}'))
    rp = RelayProvider(_cfg(), client=httpx.AsyncClient())
    ext = await rp.submit(_task())
    for _ in range(50):
        out = await rp.poll(ext)
        if out.status != "running":
            break
        await asyncio.sleep(0.01)
    assert out.status == "failed"
    assert out.error_code == "INSUFFICIENT_CREDITS"
