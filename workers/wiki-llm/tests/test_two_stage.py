"""Two-stage CoT pipeline tests (P2 #17).

Covers: stage-1 → stage-2 call sequence, wiki-context feeding into both
prompts, stage-1 failure semantics (quality gate → failed, no bare
stage-2 run), context-fetch degradation, and the
BIUMIND_WIKI_LLM_TWO_STAGE=0 single-stage kill-switch.

Runner-level tests in test_runner.py pin the single-stage shape (their
``_cfg()`` sets TWO_STAGE=0); this file pins the default-on two-stage
shape.
"""

from __future__ import annotations

import json
import uuid
from typing import AsyncIterator, List, Tuple

import pytest

from wiki_llm.brain import BrainClientError, IngestContext
from wiki_llm.config import Config
from wiki_llm.llm import LLMError
from wiki_llm.prompts import build_analyze_prompt, build_user_prompt
from wiki_llm.runner import handle_message


def _cfg_two_stage() -> Config:
    return Config.from_env({
        "BIUMIND_NATS_URL": "nats://test",
        "BIUMIND_ENV": "test",
        # 默认即开，但显式写出以钉住"默认开"的契约。
        "BIUMIND_WIKI_LLM_TWO_STAGE": "1",
    })


def _cfg_single_stage() -> Config:
    return Config.from_env({
        "BIUMIND_NATS_URL": "nats://test",
        "BIUMIND_ENV": "test",
        "BIUMIND_WIKI_LLM_TWO_STAGE": "0",
    })


def _make_request(**overrides) -> bytes:
    payload = {
        "task_id": str(uuid.uuid4()),
        "project_id": str(uuid.uuid4()),
        "owner_id": str(uuid.uuid4()),
        "raw_text": "hello world",
    }
    payload.update(overrides)
    return json.dumps(payload).encode("utf-8")


def _capture():
    captured: List[Tuple[str, dict]] = []

    async def publish(subject: str, body: bytes) -> None:
        data = json.loads(body)
        if isinstance(data, dict) and isinstance(data.get("payload"), dict):
            data = data["payload"]
        captured.append((subject, data))

    return publish, captured


def _context() -> IngestContext:
    return IngestContext(
        purpose="PURPOSE_MARKER: 本 wiki 追踪大模型推理优化。",
        schema="SCHEMA_MARKER: 概念页必须有定义与权衡两节。",
        pages=(("RoPE", "concept"), ("vLLM", "entity")),
        pages_total=5,
    )


_ONE_BLOCK = "---FILE: wiki/concepts/x.md---\n# X\nbody\n---END FILE---\n"


# ── 两阶段调用序列 + 上下文进 prompt ─────────────────────────────

@pytest.mark.asyncio
async def test_two_stage_sequence_and_context_feeding():
    """The core contract: context fetch → stage-1 (non-streaming) →
    stage-2 (streaming). Stage-1 prompt carries purpose/schema/index;
    stage-2 prompt carries the analysis + the same index."""
    publish, captured = _capture()
    calls: List[Tuple[str, str, str]] = []  # (lane, system, user)

    async def completer(system: str, user: str) -> str:
        calls.append(("stage1", system, user))
        return "## Suggested page split\nANALYSIS_MARKER: one concept page"

    async def streamer(system: str, user: str) -> AsyncIterator[str]:
        calls.append(("stage2", system, user))
        yield _ONE_BLOCK

    fetch_args: List[Tuple[str, str]] = []

    async def fetcher(project_id: str, owner_id: str) -> IngestContext:
        fetch_args.append((project_id, owner_id))
        return _context()

    body = json.loads(_make_request())
    req = await handle_message(
        json.dumps(body).encode(),
        cfg=_cfg_two_stage(), publish=publish,
        llm_stream=streamer, llm_complete=completer,
        context_fetcher=fetcher,
    )
    assert req is not None

    # 上下文拉取用 (project_id, owner_id)。
    assert fetch_args == [(body["project_id"], body["owner_id"])]

    # 调用序列：stage1 先、stage2 后，各一次。
    assert [lane for lane, _, _ in calls] == ["stage1", "stage2"]

    # Stage-1 prompt：purpose / schema / 页面索引 / 总数提示都进去。
    s1_user = calls[0][2]
    assert "PURPOSE_MARKER" in s1_user
    assert "SCHEMA_MARKER" in s1_user
    assert "- RoPE (concept)" in s1_user
    assert "- vLLM (entity)" in s1_user
    assert "and 3 more" in s1_user  # pages_total=5, index=2

    # Stage-2 prompt：分析结果 + 同一索引 + wikilink 指令。
    s2_user = calls[1][2]
    assert "ANALYSIS_MARKER" in s2_user
    assert "Planning analysis" in s2_user
    assert "- RoPE (concept)" in s2_user
    assert "[[Exact Title]]" in s2_user

    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "page", "done"]


@pytest.mark.asyncio
async def test_two_stage_works_without_context():
    """Empty context (blank project / brain unconfigured) → prompts
    simply omit the wiki sections; the pipeline still runs."""
    publish, captured = _capture()

    async def completer(system: str, user: str) -> str:
        assert "Wiki purpose" not in user
        assert "Existing wiki pages" not in user
        return "analysis without context"

    async def streamer(system: str, user: str) -> AsyncIterator[str]:
        assert "Wiki purpose" not in user
        assert "analysis without context" in user
        yield _ONE_BLOCK

    async def fetcher(project_id: str, owner_id: str) -> IngestContext:
        return IngestContext()

    await handle_message(
        _make_request(), cfg=_cfg_two_stage(), publish=publish,
        llm_stream=streamer, llm_complete=completer,
        context_fetcher=fetcher,
    )
    assert [p["kind"] for _, p in captured] == ["running", "page", "done"]


# ── Stage 1 失败语义（质量门）────────────────────────────────────

@pytest.mark.asyncio
async def test_stage1_llm_error_fails_task_and_skips_stage2():
    """Stage-1 failure must NOT degrade into a bare stage-2 run — the
    task fails and the streaming lane is never touched."""
    publish, captured = _capture()
    stream_called = False

    async def completer(system: str, user: str) -> str:
        raise LLMError("upstream 500")

    async def streamer(system: str, user: str) -> AsyncIterator[str]:
        nonlocal stream_called
        stream_called = True
        yield _ONE_BLOCK

    async def fetcher(project_id: str, owner_id: str) -> IngestContext:
        return _context()

    await handle_message(
        _make_request(), cfg=_cfg_two_stage(), publish=publish,
        llm_stream=streamer, llm_complete=completer,
        context_fetcher=fetcher,
    )
    assert not stream_called
    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "failed"]
    assert "stage-1" in captured[-1][1]["error"]
    assert "upstream 500" in captured[-1][1]["error"]


@pytest.mark.asyncio
async def test_stage1_empty_analysis_fails_task():
    """An empty analysis is a quality-gate failure too — feeding
    nothing into stage 2 would silently revert to single-stage
    quality."""
    publish, captured = _capture()
    stream_called = False

    async def completer(system: str, user: str) -> str:
        return "   "

    async def streamer(system: str, user: str) -> AsyncIterator[str]:
        nonlocal stream_called
        stream_called = True
        yield _ONE_BLOCK

    async def fetcher(project_id: str, owner_id: str) -> IngestContext:
        return IngestContext()

    await handle_message(
        _make_request(), cfg=_cfg_two_stage(), publish=publish,
        llm_stream=streamer, llm_complete=completer,
        context_fetcher=fetcher,
    )
    assert not stream_called
    assert [p["kind"] for _, p in captured] == ["running", "failed"]
    assert "quality gate" in captured[-1][1]["error"]


# ── 上下文拉取失败降级 ────────────────────────────────────────────

@pytest.mark.asyncio
async def test_context_fetch_failure_degrades_not_fails():
    """Brain unreachable / old brain without the endpoint → warn +
    empty context. Only stage-1 LLM failure is a hard gate; a missing
    context endpoint must not kill ingest during rolling deploys."""
    publish, captured = _capture()

    async def completer(system: str, user: str) -> str:
        return "degraded analysis"

    async def streamer(system: str, user: str) -> AsyncIterator[str]:
        assert "degraded analysis" in user
        yield _ONE_BLOCK

    async def fetcher(project_id: str, owner_id: str) -> IngestContext:
        raise BrainClientError("brain ingest-context returned HTTP 404")

    await handle_message(
        _make_request(), cfg=_cfg_two_stage(), publish=publish,
        llm_stream=streamer, llm_complete=completer,
        context_fetcher=fetcher,
    )
    assert [p["kind"] for _, p in captured] == ["running", "page", "done"]


# ── 单阶段回退开关 ───────────────────────────────────────────────

@pytest.mark.asyncio
async def test_two_stage_disabled_skips_stage1_and_context():
    """BIUMIND_WIKI_LLM_TWO_STAGE=0 → legacy single-stage: no context
    fetch, no completer call, prompt has no analysis section."""
    publish, captured = _capture()
    stage1_called = False
    fetch_called = False

    async def completer(system: str, user: str) -> str:
        nonlocal stage1_called
        stage1_called = True
        return "should not run"

    async def fetcher(project_id: str, owner_id: str) -> IngestContext:
        nonlocal fetch_called
        fetch_called = True
        return _context()

    async def streamer(system: str, user: str) -> AsyncIterator[str]:
        assert "Planning analysis" not in user
        # 单阶段 prompt 保留旧的 1/2/3 生成指令。
        assert "1. A source summary page" in user
        yield _ONE_BLOCK

    await handle_message(
        _make_request(), cfg=_cfg_single_stage(), publish=publish,
        llm_stream=streamer, llm_complete=completer,
        context_fetcher=fetcher,
    )
    assert not stage1_called
    assert not fetch_called
    assert [p["kind"] for _, p in captured] == ["running", "page", "done"]


def test_two_stage_env_parsing():
    """开关解析：默认开；0/false/no/off 关闭（大小写不敏感）。"""
    base = {"BIUMIND_NATS_URL": "nats://t"}
    assert Config.from_env(base).two_stage is True
    for off in ("0", "false", "NO", "Off"):
        assert Config.from_env(
            {**base, "BIUMIND_WIKI_LLM_TWO_STAGE": off}).two_stage is False
    assert Config.from_env(
        {**base, "BIUMIND_WIKI_LLM_TWO_STAGE": "1"}).two_stage is True


# ── prompt 单元级 ────────────────────────────────────────────────

def test_analyze_prompt_structure():
    user = build_analyze_prompt(
        source_title="T", source_text="body", context=_context(),
    )
    for section in ("## Entities", "## Concepts",
                    "## Related existing pages",
                    "## Potential conflicts",
                    "## Suggested page split"):
        assert section in user, f"missing section {section}"
    assert "≤ 600 words" in user  # 成本控制写死在 prompt 里
    assert "body" in user


def test_user_prompt_legacy_shape_without_analysis():
    """无 analysis/context 时 stage-2 prompt 退回 P1-8 形状。"""
    user = build_user_prompt(source_title="T", source_text="body")
    assert "Planning analysis" not in user
    assert "Wiki purpose" not in user
    assert "1. A source summary page" in user
    assert "---FILE:" in user


def test_ingest_context_from_payload():
    ctx = IngestContext.from_payload({
        "purpose": "p", "schema": "s",
        "pages": [{"title": "A", "type": "concept"},
                  {"title": "", "type": "x"},  # 空标题被丢
                  "garbage"],
        "pages_total": 7,
    })
    assert ctx.purpose == "p" and ctx.schema == "s"
    assert ctx.pages == (("A", "concept"),)
    assert ctx.pages_total == 7
    # 缺字段容忍（旧 brain 不返回新字段）。
    empty = IngestContext.from_payload({})
    assert empty.pages == () and empty.pages_total == 0
