"""Unit tests for biumind_aigc.worker — 不依赖 NATS 真服务.

测试用 stub publish 闭包 + StubProvider 验证完整状态机:
  submit msg → queued → running → completed (含 outputs)
  并验证 failed / blocked / timeout / bad provider 各分支.
"""

from __future__ import annotations

import asyncio
import json
from typing import Any

import pytest

from biumind_aigc.config import Config
from biumind_aigc.event import SubmitTask
from biumind_aigc.providers.base import (
    Executor,
    Outcome,
    ProviderError,
    ProviderUnavailable,
)
from biumind_aigc.worker import handle_submit


# ─── helpers ──────────────────────────────────────────


def cfg() -> Config:
    return Config.from_env({
        "NATS_URL": "nats://x",
        "BIUMIND_AIGC_TIMEOUT_S": "5",
        "BIUMIND_AIGC_POLL_INTERVAL_S": "0",  # 测试不睡
    })


class CapturePublisher:
    """记录每次 publish 的 (subject, body) — 取代真 NATS."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, bytes]] = []

    async def __call__(self, subject: str, body: bytes) -> None:
        self.calls.append((subject, body))

    def updates(self) -> list[dict[str, Any]]:
        return [json.loads(b.decode("utf-8")) for _, b in self.calls]


class _Factory:
    """provider_factory closure helper for testing arbitrary executors."""

    def __init__(self, executor: Executor):
        self.executor = executor

    def __call__(self, code: str, c: Config) -> Executor:
        return self.executor


def submit_msg(**overrides) -> bytes:
    base = {
        "task_id": "11111111-1111-1111-1111-111111111111",
        "user_id": "22222222-2222-2222-2222-222222222222",
        "type": "image",
        "model_code": "test-model",
        "provider_code": "stub",
        "prompt": "柯基",
        "cost_credits": 30,
    }
    base.update(overrides)
    return json.dumps(base).encode("utf-8")


# ─── Stub-provider happy path ─────────────────────────


async def test_handle_submit_completed_with_stub() -> None:
    pub = CapturePublisher()
    msg = submit_msg(provider_code="stub")
    result = await handle_submit(msg, cfg=cfg(), publish=pub)

    assert result is not None
    assert result.status == "completed"

    # 应该至少 publish: queued + completed
    statuses = [u["status"] for u in pub.updates()]
    assert "queued" in statuses
    assert statuses[-1] == "completed"

    completed = pub.updates()[-1]
    assert completed["progress"] == 100
    assert len(completed["outputs"]) == 1
    assert completed["outputs"][0]["storage_url"].startswith("https://stub.local/")
    assert completed["external_task_id"].startswith("stub-")


# ─── progress 单调递增 ────────────────────────────────


class _RunningThenComplete(Executor):
    def __init__(self) -> None:
        self.calls = 0

    @property
    def code(self) -> str:
        return "running-then-complete"

    async def submit(self, task) -> str:
        return "ext-1"

    async def poll(self, external_id: str) -> Outcome:
        self.calls += 1
        if self.calls < 3:
            return Outcome(status="running", progress=self.calls * 30)
        return Outcome(
            status="completed", progress=100,
            output_urls=["https://x.local/a.png"],
        )


async def test_progress_monotone_and_emits_running() -> None:
    pub = CapturePublisher()
    exe = _RunningThenComplete()
    result = await handle_submit(submit_msg(), cfg=cfg(), publish=pub,
                                  provider_factory=_Factory(exe))
    assert result.status == "completed"

    progs = [u.get("progress", 0) for u in pub.updates()]
    # 单调递增 (允许相等)
    for i in range(1, len(progs)):
        assert progs[i] >= progs[i - 1], f"progress 回退: {progs}"


# ─── Failed / Blocked / Timeout ───────────────────────


class _SubmitFails(Executor):
    @property
    def code(self) -> str:
        return "fail-on-submit"

    async def submit(self, task) -> str:
        raise ProviderError("limit exceeded")

    async def poll(self, external_id: str) -> Outcome:
        raise NotImplementedError


async def test_submit_failure_emits_failed_with_refund() -> None:
    pub = CapturePublisher()
    result = await handle_submit(
        submit_msg(cost_credits=40),
        cfg=cfg(), publish=pub,
        provider_factory=_Factory(_SubmitFails()),
    )
    assert result.status == "failed"
    assert result.error_code == "UPSTREAM_SUBMIT"
    last = pub.updates()[-1]
    assert last["status"] == "failed"
    assert last["refunded_credits"] == 40
    assert "limit exceeded" in last["error_message"]


class _Blocked(Executor):
    @property
    def code(self) -> str:
        return "blocked"

    async def submit(self, task) -> str:
        return "ext-blocked"

    async def poll(self, external_id: str) -> Outcome:
        return Outcome(status="blocked", error_message="contains banned content")


async def test_blocked_status() -> None:
    pub = CapturePublisher()
    result = await handle_submit(
        submit_msg(cost_credits=20),
        cfg=cfg(), publish=pub,
        provider_factory=_Factory(_Blocked()),
    )
    assert result.status == "blocked"
    last = pub.updates()[-1]
    assert last["status"] == "blocked"
    assert last["error_code"] == "MODERATION_BLOCKED"
    assert last["refunded_credits"] == 20


class _NeverCompletes(Executor):
    @property
    def code(self) -> str:
        return "never"

    async def submit(self, task) -> str:
        return "ext-never"

    async def poll(self, external_id: str) -> Outcome:
        return Outcome(status="running", progress=10)


async def test_timeout_emits_failed() -> None:
    """timeout=0 让循环立即超时."""
    c = Config.from_env({"NATS_URL": "x", "BIUMIND_AIGC_TIMEOUT_S": "0",
                         "BIUMIND_AIGC_POLL_INTERVAL_S": "0"})
    pub = CapturePublisher()
    result = await handle_submit(
        submit_msg(),
        cfg=c, publish=pub,
        provider_factory=_Factory(_NeverCompletes()),
    )
    assert result.status == "failed"
    assert result.error_code == "TIMEOUT"


# ─── 不存在的 provider ────────────────────────────────


def _unknown_factory(code: str, c: Config) -> Executor:
    raise ProviderUnavailable(f"unknown provider {code}")


async def test_unknown_provider_emits_failed() -> None:
    pub = CapturePublisher()
    result = await handle_submit(
        submit_msg(provider_code="not-registered", cost_credits=15),
        cfg=cfg(), publish=pub, provider_factory=_unknown_factory,
    )
    assert result.status == "failed"
    assert result.error_code == "PROVIDER_UNAVAILABLE"
    last = pub.updates()[-1]
    assert last["refunded_credits"] == 15


# ─── 坏消息体 ─────────────────────────────────────────


async def test_bad_json_returns_none() -> None:
    pub = CapturePublisher()
    result = await handle_submit(b"{not json", cfg=cfg(), publish=pub)
    assert result is None
    assert pub.calls == [], "不该 publish 任何事件"


async def test_missing_required_field_returns_none() -> None:
    pub = CapturePublisher()
    bad = json.dumps({"task_id": "x"}).encode("utf-8")  # 缺 user_id / type / model_code 等
    result = await handle_submit(bad, cfg=cfg(), publish=pub)
    assert result is None


# ─── P4.S3.2 — credential 注入到 cfg 路径 ────────────────


async def test_payload_credential_overrides_env() -> None:
    """Payload 带 credential_api_key 时, provider_factory 收到的 cfg
    应该用 task 里的 key 覆盖 env. 验证 has_payload_credential() 路径."""
    pub = CapturePublisher()
    captured_keys: list[str] = []

    class CapturingExec(Executor):
        @property
        def code(self) -> str:
            return "stub"

        async def submit(self, task) -> str:
            return "ext-1"

        async def poll(self, external_id: str):
            return Outcome(
                status="completed", progress=100, error_code="",
                error_message="", outputs=[("https://x/y.png", "image")],
                external_id=external_id,
            )

        async def aclose(self) -> None: ...

    exe = CapturingExec()

    def factory(code: str, c: Config) -> Executor:
        captured_keys.append(c.dashscope_api_key)
        return exe

    msg = submit_msg(
        provider_code="dashscope",
        credential_api_key="sk-from-payload",
        credential_base_url="https://dashscope.x",
        credential_last4="oad",
    )
    await handle_submit(msg, cfg=cfg(), publish=pub, provider_factory=factory)

    assert captured_keys == ["sk-from-payload"], (
        f"factory should have received payload-injected key, got {captured_keys}"
    )


async def test_no_payload_credential_falls_back_to_env() -> None:
    """payload 缺 credential_api_key 时, factory 应收到 env 配的 cfg."""
    env_cfg = Config.from_env({
        "NATS_URL": "nats://x",
        "DASHSCOPE_API_KEY": "sk-from-env",
        "BIUMIND_AIGC_POLL_INTERVAL_S": "0",
    })
    pub = CapturePublisher()
    captured_keys: list[str] = []

    class StubExec(Executor):
        @property
        def code(self) -> str:
            return "stub"

        async def submit(self, task) -> str:
            return "ext-2"

        async def poll(self, external_id: str):
            return Outcome(
                status="completed", progress=100, error_code="",
                error_message="", outputs=[("https://x/y.png", "image")],
                external_id=external_id,
            )

        async def aclose(self) -> None: ...

    def factory(code: str, c: Config) -> Executor:
        captured_keys.append(c.dashscope_api_key)
        return StubExec()

    # No credential_api_key in payload — should use env.
    msg = submit_msg(provider_code="dashscope")
    await handle_submit(msg, cfg=env_cfg, publish=pub, provider_factory=factory)

    assert captured_keys == ["sk-from-env"], (
        f"factory should have received env key when payload empty, got {captured_keys}"
    )


def test_submit_task_parses_credential_fields() -> None:
    """SubmitTask.from_json: credential_* 字段非空时填充, 缺失时空字符串."""
    raw = json.dumps({
        "task_id": "t1", "user_id": "u1", "type": "image",
        "model_code": "m", "provider_code": "p", "prompt": "x",
        "cost_credits": 0,
        "credential_api_key": "sk-12345",
        "credential_base_url": "https://api",
        "credential_headers": {"X": "Y"},
        "credential_last4": "2345",
        "hold_id": "hold-abc",
    })
    t = SubmitTask.from_json(raw)
    assert t.credential_api_key == "sk-12345"
    assert t.credential_base_url == "https://api"
    assert t.credential_headers == {"X": "Y"}
    assert t.credential_last4 == "2345"
    assert t.hold_id == "hold-abc"
    assert t.has_payload_credential() is True


def test_submit_task_credential_fields_default_empty() -> None:
    """payload 不带 credential_* 时, has_payload_credential 应为 False."""
    raw = json.dumps({
        "task_id": "t1", "user_id": "u1", "type": "image",
        "model_code": "m", "provider_code": "p", "prompt": "x",
        "cost_credits": 0,
    })
    t = SubmitTask.from_json(raw)
    assert t.credential_api_key == ""
    assert t.credential_headers == {}
    assert t.has_payload_credential() is False
