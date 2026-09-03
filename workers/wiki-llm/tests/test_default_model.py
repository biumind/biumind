"""Default chat model resolution tests (B3).

Covers the relay/builtin tiers of the priority chain pinned by
``runner._resolve_model``:

  1. ``BIUMIND_WIKI_LLM_MODEL`` env explicit override — wins, endpoint
     is never consulted;
  2. relay ``GET /v1/internal/models/default-chat`` — admin-designated
     default, process-cached (60s positive / 10s negative TTL, aligned
     with brain's ``agentplane.DefaultModelResolver``);
  3. endpoint failure (404 / 5xx / network / disabled resolver) —
     falls back to the built-in default, never raises (aligned with
     brain ChatRunner: relay down must not kill the pipeline).

The per-owner preference tier inserted between 1 and 2 (B2, identity
internal endpoint) is covered in test_preference_model.py.

HTTP is mocked with ``httpx.MockTransport``; the resolver clock is
injected so TTL tests don't sleep.
"""

from __future__ import annotations

import json
import uuid
from typing import List, Tuple

import httpx

from wiki_llm.config import BUILTIN_FALLBACK_MODEL, Config
from wiki_llm.default_model import DefaultModelResolver
from wiki_llm.runner import _resolve_model, handle_message


def _cfg(**env) -> Config:
    base = {
        "BIUMIND_NATS_URL": "nats://test",
        "BIUMIND_ENV": "test",
    }
    base.update(env)
    return Config.from_env(base)


class _Clock:
    """Controllable monotonic clock for TTL tests."""

    def __init__(self) -> None:
        self.now = 1000.0

    def __call__(self) -> float:
        return self.now

    def advance(self, seconds: float) -> None:
        self.now += seconds


def _mock_transport(
    handler, requests: List[httpx.Request],
) -> httpx.MockTransport:
    def _record(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        return handler(request)

    return httpx.MockTransport(_record)


# ── resolver 单元:端点契约 + 缓存 ─────────────────────────────


async def test_resolver_returns_code_and_sends_bearer():
    requests: List[httpx.Request] = []
    transport = _mock_transport(
        lambda req: httpx.Response(200, json={"code": "anthropic.claude-opus-4-8"}),
        requests,
    )
    r = DefaultModelResolver(
        "http://relay:7001/", "tok", transport=transport,
    )
    assert await r.default_chat_model() == "anthropic.claude-opus-4-8"
    assert len(requests) == 1
    req = requests[0]
    assert req.url.path == "/v1/internal/models/default-chat"
    assert req.headers["Authorization"] == "Bearer tok"


async def test_resolver_positive_cache_avoids_second_fetch():
    requests: List[httpx.Request] = []
    transport = _mock_transport(
        lambda req: httpx.Response(200, json={"code": "m1"}), requests,
    )
    clock = _Clock()
    r = DefaultModelResolver(
        "http://relay", "tok", transport=transport, clock=clock,
    )
    assert await r.default_chat_model() == "m1"
    clock.advance(30)  # within 60s positive TTL
    assert await r.default_chat_model() == "m1"
    assert len(requests) == 1
    clock.advance(31)  # TTL expired → refetch
    assert await r.default_chat_model() == "m1"
    assert len(requests) == 2


async def test_resolver_404_is_negative_cached():
    requests: List[httpx.Request] = []
    transport = _mock_transport(
        lambda req: httpx.Response(404, text="no default chat model"),
        requests,
    )
    clock = _Clock()
    r = DefaultModelResolver(
        "http://relay", "tok", transport=transport, clock=clock,
    )
    assert await r.default_chat_model() == ""
    clock.advance(5)  # within 10s negative TTL → no refetch
    assert await r.default_chat_model() == ""
    assert len(requests) == 1
    clock.advance(6)  # negative TTL expired → retry
    assert await r.default_chat_model() == ""
    assert len(requests) == 2


async def test_resolver_5xx_returns_empty():
    transport = _mock_transport(
        lambda req: httpx.Response(503, text="registry cache not wired"),
        [],
    )
    r = DefaultModelResolver("http://relay", "tok", transport=transport)
    assert await r.default_chat_model() == ""


async def test_resolver_network_error_returns_empty():
    def _boom(req: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    r = DefaultModelResolver(
        "http://relay", "tok", transport=httpx.MockTransport(_boom),
    )
    assert await r.default_chat_model() == ""


async def test_resolver_disabled_without_url_or_token():
    def _boom(req: httpx.Request) -> httpx.Response:
        raise AssertionError("disabled resolver must not fetch")

    transport = httpx.MockTransport(_boom)
    assert await DefaultModelResolver(
        "", "tok", transport=transport).default_chat_model() == ""
    assert await DefaultModelResolver(
        "http://relay", "", transport=transport).default_chat_model() == ""


# ── runner 兜底链三态 ──────────────────────────────────────────


async def test_resolve_model_env_override_wins():
    def _boom(req: httpx.Request) -> httpx.Response:
        raise AssertionError("env override must not consult the endpoint")

    cfg = _cfg(BIUMIND_WIKI_LLM_MODEL="env.model-override")
    resolver = DefaultModelResolver(
        "http://relay", "tok", transport=httpx.MockTransport(_boom),
    )
    assert await _resolve_model(cfg, resolver) == ("env.model-override", "env")


async def test_resolve_model_pulls_from_endpoint():
    transport = _mock_transport(
        lambda req: httpx.Response(200, json={"code": "anthropic.claude-opus-4-8"}),
        [],
    )
    cfg = _cfg(
        BIUMIND_HUB_URL="http://relay:7001",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    resolver = DefaultModelResolver(
        cfg.hub_url, cfg.relay_internal_token, transport=transport,
    )
    assert await _resolve_model(cfg, resolver) == ("anthropic.claude-opus-4-8",
                                                   "default")


async def test_resolve_model_endpoint_failure_falls_back_to_builtin():
    transport = _mock_transport(
        lambda req: httpx.Response(500, text="boom"), [],
    )
    cfg = _cfg(
        BIUMIND_HUB_URL="http://relay:7001",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    resolver = DefaultModelResolver(
        cfg.hub_url, cfg.relay_internal_token, transport=transport,
    )
    assert await _resolve_model(cfg, resolver) == (BUILTIN_FALLBACK_MODEL,
                                                   "builtin")


async def test_resolve_model_no_resolver_falls_back_to_builtin():
    # hub_url 空 → 现场构造的 resolver 禁用 → 落内置兜底,不报错。
    cfg = _cfg()
    assert await _resolve_model(cfg, None) == (BUILTIN_FALLBACK_MODEL, "builtin")


# ── handle_message 端到端:解析出的模型进 LLMConfig ────────────


async def test_handle_message_feeds_resolved_model_to_llm(
    monkeypatch,
):
    from wiki_llm import runner as runner_mod

    seen_models: List[str] = []

    class _FakeStream:
        def __init__(self, llm_cfg, **kwargs):
            seen_models.append(llm_cfg.model)

        async def __aiter__(self):
            yield "---FILE: wiki/x.md---\n# X\nbody\n---END FILE---\n"

    # stream_messages 是异步生成器函数;stub 成返回 _FakeStream 的普通函数。
    monkeypatch.setattr(
        runner_mod, "stream_messages",
        lambda llm_cfg, *, system, user: _FakeStream(llm_cfg),
    )

    transport = _mock_transport(
        lambda req: httpx.Response(200, json={"code": "relay.default-model"}),
        [],
    )
    cfg = _cfg(
        BIUMIND_WIKI_LLM_TWO_STAGE="0",
        BIUMIND_HUB_URL="http://relay:7001",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    resolver = DefaultModelResolver(
        cfg.hub_url, cfg.relay_internal_token, transport=transport,
    )

    captured: List[Tuple[str, dict]] = []

    async def publish(subject: str, body: bytes) -> None:
        data = json.loads(body)
        if isinstance(data, dict) and isinstance(data.get("payload"), dict):
            data = data["payload"]
        captured.append((subject, data))

    payload = json.dumps({
        "task_id": str(uuid.uuid4()),
        "project_id": str(uuid.uuid4()),
        "owner_id": str(uuid.uuid4()),
        "raw_text": "hello world",
    }).encode("utf-8")

    req = await handle_message(
        payload, cfg=cfg, publish=publish, model_resolver=resolver,
    )
    assert req is not None
    assert seen_models == ["relay.default-model"]
    kinds = [data["kind"] for _, data in captured]
    assert "done" in kinds


async def test_handle_message_env_override_skips_endpoint(monkeypatch):
    from wiki_llm import runner as runner_mod

    seen_models: List[str] = []
    monkeypatch.setattr(
        runner_mod, "stream_messages",
        lambda llm_cfg, *, system, user: _stream_one_block(llm_cfg, seen_models),
    )

    def _boom(req: httpx.Request) -> httpx.Response:
        raise AssertionError("env override must not consult the endpoint")

    cfg = _cfg(
        BIUMIND_WIKI_LLM_TWO_STAGE="0",
        BIUMIND_WIKI_LLM_MODEL="env.forced-model",
        BIUMIND_HUB_URL="http://relay:7001",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    resolver = DefaultModelResolver(
        cfg.hub_url, cfg.relay_internal_token,
        transport=httpx.MockTransport(_boom),
    )

    async def publish(subject: str, body: bytes) -> None:
        pass

    payload = json.dumps({
        "task_id": str(uuid.uuid4()),
        "project_id": str(uuid.uuid4()),
        "owner_id": str(uuid.uuid4()),
        "raw_text": "hello world",
    }).encode("utf-8")

    req = await handle_message(
        payload, cfg=cfg, publish=publish, model_resolver=resolver,
    )
    assert req is not None
    assert seen_models == ["env.forced-model"]


def _stream_one_block(llm_cfg, seen: List[str]):
    seen.append(llm_cfg.model)

    async def _gen():
        yield "---FILE: wiki/x.md---\n# X\nbody\n---END FILE---\n"

    return _gen()
