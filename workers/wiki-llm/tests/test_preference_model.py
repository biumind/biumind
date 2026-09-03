"""Per-owner ingest-model preference tests (B2).

Covers the preference tier inserted between the env override and the
relay default-chat endpoint in ``runner._resolve_model``:

  1. ``BIUMIND_WIKI_LLM_MODEL`` env explicit override — still wins,
     identity is never consulted;
  2. identity ``GET /v1/internal/settings/{owner_id}/ingest-model`` —
     task owner's personal preference, per-owner process cache
     (60s positive / 10s negative TTL, mirroring default_model.py);
  3. preference miss (404 / network / disabled layer) — falls through
     to the relay default-chat tier, never raises;
  4. weak-model quality fallback — when the resolved model's source is
     ``preference`` and the task fails, the runner re-resolves with the
     preference layer skipped and reruns the full stage1+stage2 once
     (stage-2 idempotency key gains a ``:fallback`` suffix so the relay
     Hold dedup doesn't swallow the retry). Non-preference sources
     (env/default/builtin) never rerun.

HTTP is mocked with ``httpx.MockTransport``; the resolver clock is
injected so TTL tests don't sleep.
"""

from __future__ import annotations

import json
import uuid
from typing import Dict, List, Tuple

import httpx

from wiki_llm.config import Config
from wiki_llm.default_model import DefaultModelResolver
from wiki_llm.llm import LLMError
from wiki_llm.preference_model import PreferenceModelResolver
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


def _mock_transport(handler, requests: List[httpx.Request]):
    def _record(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        return handler(request)

    return httpx.MockTransport(_record)


_OWNER_A = str(uuid.uuid4())
_OWNER_B = str(uuid.uuid4())

_ONE_BLOCK = "---FILE: wiki/x.md---\n# X\nbody\n---END FILE---\n"


# ── resolver 单元:端点契约 + per-owner 缓存 ──────────────────────


async def test_resolver_returns_model_and_sends_bearer():
    requests: List[httpx.Request] = []
    transport = _mock_transport(
        lambda req: httpx.Response(200, json={"model": "openai.gpt-5.2"}),
        requests,
    )
    r = PreferenceModelResolver(
        "http://identity:7004/", "tok", transport=transport,
    )
    assert await r.preference_model(_OWNER_A) == "openai.gpt-5.2"
    assert len(requests) == 1
    req = requests[0]
    assert req.url.path == (f"/v1/internal/settings/{_OWNER_A}/ingest-model")
    assert req.headers["Authorization"] == "Bearer tok"


async def test_resolver_caches_per_owner():
    models: Dict[str, str] = {_OWNER_A: "model-a", _OWNER_B: "model-b"}
    requests: List[httpx.Request] = []

    def _handler(req: httpx.Request) -> httpx.Response:
        owner = req.url.path.split("/")[4]
        return httpx.Response(200, json={"model": models[owner]})

    transport = _mock_transport(_handler, requests)
    clock = _Clock()
    r = PreferenceModelResolver(
        "http://identity", "tok", transport=transport, clock=clock,
    )
    assert await r.preference_model(_OWNER_A) == "model-a"
    assert await r.preference_model(_OWNER_B) == "model-b"
    clock.advance(30)  # within 60s positive TTL → both cached
    assert await r.preference_model(_OWNER_A) == "model-a"
    assert await r.preference_model(_OWNER_B) == "model-b"
    assert len(requests) == 2
    clock.advance(31)  # TTL expired → refetch per owner
    assert await r.preference_model(_OWNER_A) == "model-a"
    assert len(requests) == 3


async def test_resolver_404_is_negative_cached_per_owner():
    requests: List[httpx.Request] = []
    transport = _mock_transport(
        lambda req: httpx.Response(404, text="preference not set"),
        requests,
    )
    clock = _Clock()
    r = PreferenceModelResolver(
        "http://identity", "tok", transport=transport, clock=clock,
    )
    assert await r.preference_model(_OWNER_A) == ""
    clock.advance(5)  # within 10s negative TTL → no refetch
    assert await r.preference_model(_OWNER_A) == ""
    assert len(requests) == 1
    # 负缓存是 per-owner 的:另一个 owner 不受影响,正常发起查询。
    assert await r.preference_model(_OWNER_B) == ""
    assert len(requests) == 2
    clock.advance(6)  # negative TTL expired → retry
    assert await r.preference_model(_OWNER_A) == ""
    assert len(requests) == 3


async def test_resolver_5xx_and_network_error_return_empty():
    r = PreferenceModelResolver(
        "http://identity", "tok",
        transport=httpx.MockTransport(
            lambda req: httpx.Response(500, text="boom")),
    )
    assert await r.preference_model(_OWNER_A) == ""

    def _boom(req: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    r = PreferenceModelResolver(
        "http://identity", "tok", transport=httpx.MockTransport(_boom),
    )
    assert await r.preference_model(_OWNER_A) == ""


async def test_resolver_disabled_without_url_or_token():
    def _boom(req: httpx.Request) -> httpx.Response:
        raise AssertionError("disabled resolver must not fetch")

    transport = httpx.MockTransport(_boom)
    assert await PreferenceModelResolver(
        "", "tok", transport=transport).preference_model(_OWNER_A) == ""
    assert await PreferenceModelResolver(
        "http://identity", "", transport=transport,
    ).preference_model(_OWNER_A) == ""


# ── runner 解析链:偏好层优先级与回落 ─────────────────────────────


def _pref_resolver(handler, requests=None):
    return PreferenceModelResolver(
        "http://identity:7004", "tok",
        transport=_mock_transport(handler, requests if requests is not None else []),
    )


def _default_resolver(code: str):
    return DefaultModelResolver(
        "http://relay:7001", "tok",
        transport=httpx.MockTransport(
            lambda req: httpx.Response(200, json={"code": code})),
    )


async def test_env_override_wins_over_preference():
    def _boom(req: httpx.Request) -> httpx.Response:
        raise AssertionError("env override must not consult identity")

    cfg = _cfg(BIUMIND_WIKI_LLM_MODEL="env.model-override")
    pref = PreferenceModelResolver(
        "http://identity", "tok", transport=httpx.MockTransport(_boom),
    )
    assert await _resolve_model(
        cfg, _default_resolver("relay.default"), _OWNER_A, pref,
    ) == ("env.model-override", "env")


async def test_preference_hit_wins_over_default_chat():
    pref = _pref_resolver(
        lambda req: httpx.Response(200, json={"model": "pref.user-model"}),
    )
    cfg = _cfg(
        BIUMIND_IDENTITY_URL="http://identity:7004",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    assert await _resolve_model(
        cfg, _default_resolver("relay.default"), _OWNER_A, pref,
    ) == ("pref.user-model", "preference")


async def test_preference_404_falls_through_to_default_chat():
    pref = _pref_resolver(
        lambda req: httpx.Response(404, text="preference not set"),
    )
    cfg = _cfg(
        BIUMIND_IDENTITY_URL="http://identity:7004",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    assert await _resolve_model(
        cfg, _default_resolver("relay.default"), _OWNER_A, pref,
    ) == ("relay.default", "default")


async def test_preference_network_failure_falls_through_to_default_chat():
    def _boom(req: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    pref = _pref_resolver(_boom)
    cfg = _cfg(
        BIUMIND_IDENTITY_URL="http://identity:7004",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    assert await _resolve_model(
        cfg, _default_resolver("relay.default"), _OWNER_A, pref,
    ) == ("relay.default", "default")


async def test_skip_preference_bypasses_identity():
    def _boom(req: httpx.Request) -> httpx.Response:
        raise AssertionError("skip_preference must not consult identity")

    pref = _pref_resolver(_boom)
    cfg = _cfg(
        BIUMIND_IDENTITY_URL="http://identity:7004",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
    )
    assert await _resolve_model(
        cfg, _default_resolver("relay.default"), _OWNER_A, pref,
        skip_preference=True,
    ) == ("relay.default", "default")


# ── handle_message:弱模型质量兜底(降级重跑)──────────────────────


def _capture():
    captured: List[Tuple[str, dict]] = []

    async def publish(subject: str, body: bytes) -> None:
        data = json.loads(body)
        if isinstance(data, dict) and isinstance(data.get("payload"), dict):
            data = data["payload"]
        captured.append((subject, data))

    return publish, captured


def _make_request(**overrides) -> dict:
    payload = {
        "task_id": str(uuid.uuid4()),
        "project_id": str(uuid.uuid4()),
        "owner_id": _OWNER_A,
        "raw_text": "hello world",
    }
    payload.update(overrides)
    return payload


class _ScriptedLLM:
    """记录每次调用的 (model, idempotency_key),按脚本逐次产出。

    ``scripts`` 里每项是 delta 列表;以 ``LLMError`` 实例开头表示该次
    调用直接抛错。供 monkeypatch 替换 runner 的 stream_messages /
    complete_messages。
    """

    def __init__(self, *scripts) -> None:
        self._scripts = list(scripts)
        self.calls: List[Tuple[str, str]] = []

    def stream(self, llm_cfg, *, system, user):
        self.calls.append((llm_cfg.model, llm_cfg.idempotency_key))
        deltas = self._scripts.pop(0)

        async def _gen():
            for d in deltas:
                if isinstance(d, LLMError):
                    raise d
                yield d

        return _gen()

    async def complete(self, llm_cfg, *, system, user) -> str:
        self.calls.append((llm_cfg.model, llm_cfg.idempotency_key))
        deltas = self._scripts.pop(0)
        out = []
        for d in deltas:
            if isinstance(d, LLMError):
                raise d
            out.append(d)
        return "".join(out)


def _fallback_cfg(**env) -> Config:
    return _cfg(
        BIUMIND_HUB_URL="http://relay:7001",
        BIUMIND_RELAY_INTERNAL_TOKEN="tok",
        BIUMIND_IDENTITY_URL="http://identity:7004",
        **env,
    )


async def test_preference_failure_reruns_once_with_fallback_key(monkeypatch):
    """偏好层模型产出零 FILE 块(失败)→ 去掉偏好层重解析,用 relay
    default-chat 重跑一次;stage-2 idempotency_key 带 :fallback。"""
    from wiki_llm import runner as runner_mod

    llm = _ScriptedLLM(
        ["I'm sorry, I cannot help with that.\n"],  # attempt 1: 零块 → 失败
        [_ONE_BLOCK],                               # attempt 2: 成功
    )
    monkeypatch.setattr(runner_mod, "stream_messages", llm.stream)

    publish, captured = _capture()
    body = _make_request()
    pref = _pref_resolver(
        lambda req: httpx.Response(200, json={"model": "pref.weak-model"}),
    )
    req = await handle_message(
        json.dumps(body).encode("utf-8"),
        cfg=_fallback_cfg(BIUMIND_WIKI_LLM_TWO_STAGE="0"),
        publish=publish,
        model_resolver=_default_resolver("relay.default-model"),
        preference_resolver=pref,
    )
    assert req is not None
    assert llm.calls == [
        ("pref.weak-model", body["task_id"]),
        ("relay.default-model", body["task_id"] + ":fallback"),
    ]
    assert [p["kind"] for _, p in captured] == ["running", "page", "done"]


async def test_preference_failure_both_attempts_fail_emits_failed_once(
    monkeypatch,
):
    """重跑仍失败才发 failed;错误文本如实反映已降级重试过一次。"""
    from wiki_llm import runner as runner_mod

    llm = _ScriptedLLM(
        ["prose, no blocks\n"],
        ["still prose\n"],
    )
    monkeypatch.setattr(runner_mod, "stream_messages", llm.stream)

    publish, captured = _capture()
    body = _make_request()
    pref = _pref_resolver(
        lambda req: httpx.Response(200, json={"model": "pref.weak-model"}),
    )
    await handle_message(
        json.dumps(body).encode("utf-8"),
        cfg=_fallback_cfg(BIUMIND_WIKI_LLM_TWO_STAGE="0"),
        publish=publish,
        model_resolver=_default_resolver("relay.default-model"),
        preference_resolver=pref,
    )
    assert len(llm.calls) == 2
    assert [p["kind"] for _, p in captured] == ["running", "failed"]
    error = captured[-1][1]["error"]
    assert "no parseable FILE blocks" in error
    assert "retried once" in error
    assert "relay.default-model" in error


async def test_non_preference_source_failure_does_not_rerun(monkeypatch):
    """env 覆盖来源失败不重跑 —— 运维显式选择,重跑只是浪费配额;
    且 env 覆盖下 identity 不应被查询。"""
    from wiki_llm import runner as runner_mod

    llm = _ScriptedLLM(["prose, no blocks\n"])
    monkeypatch.setattr(runner_mod, "stream_messages", llm.stream)

    def _boom(req: httpx.Request) -> httpx.Response:
        raise AssertionError("env override must not consult identity")

    publish, captured = _capture()
    body = _make_request()
    pref = PreferenceModelResolver(
        "http://identity", "tok", transport=httpx.MockTransport(_boom),
    )
    await handle_message(
        json.dumps(body).encode("utf-8"),
        cfg=_fallback_cfg(
            BIUMIND_WIKI_LLM_TWO_STAGE="0",
            BIUMIND_WIKI_LLM_MODEL="env.forced-model",
        ),
        publish=publish,
        model_resolver=_default_resolver("relay.default-model"),
        preference_resolver=pref,
    )
    assert llm.calls == [("env.forced-model", body["task_id"])]
    assert [p["kind"] for _, p in captured] == ["running", "failed"]
    assert "retried once" not in captured[-1][1]["error"]


async def test_preference_stage1_failure_reruns_with_fallback_keys(
    monkeypatch,
):
    """两阶段下 stage-1 LLMError 同样触发降级重跑;stage-1 重跑的
    idempotency_key 是 task:fallback:analyze,stage-2 是 task:fallback。"""
    from wiki_llm import runner as runner_mod

    completer = _ScriptedLLM(
        [LLMError("upstream 500")],  # attempt 1 stage-1: 失败
        ["fallback analysis"],       # attempt 2 stage-1: 成功
    )
    streamer = _ScriptedLLM([_ONE_BLOCK])
    monkeypatch.setattr(runner_mod, "complete_messages", completer.complete)
    monkeypatch.setattr(runner_mod, "stream_messages", streamer.stream)

    publish, captured = _capture()
    body = _make_request()
    pref = _pref_resolver(
        lambda req: httpx.Response(200, json={"model": "pref.weak-model"}),
    )
    await handle_message(
        json.dumps(body).encode("utf-8"),
        cfg=_fallback_cfg(BIUMIND_WIKI_LLM_TWO_STAGE="1"),
        publish=publish,
        model_resolver=_default_resolver("relay.default-model"),
        preference_resolver=pref,
    )
    tid = body["task_id"]
    assert completer.calls == [
        ("pref.weak-model", tid + ":analyze"),
        ("relay.default-model", tid + ":fallback:analyze"),
    ]
    assert streamer.calls == [("relay.default-model", tid + ":fallback")]
    assert [p["kind"] for _, p in captured] == ["running", "page", "done"]
