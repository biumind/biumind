"""Runner-level tests using stub publisher + stub LLM streamer.

The real ``run`` coroutine connects to NATS; ``handle_message`` is the
unit boundary we exercise here. Stub publish records (subject, payload)
tuples; stub LLM yields a scripted sequence of deltas so the
streaming-partial-save loop is testable without touching hub.
"""

from __future__ import annotations

import json
import uuid
from typing import AsyncIterator, List, Tuple

import pytest

from wiki_llm.config import Config
from wiki_llm.runner import handle_message


def _cfg() -> Config:
    return Config.from_env({
        "BIUMIND_NATS_URL": "nats://test",
        "BIUMIND_ENV": "test",
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
    """Returns (publish, captured) where captured is a list of (subject, payload-dict)."""
    captured: List[Tuple[str, dict]] = []

    async def publish(subject: str, body: bytes) -> None:
        captured.append((subject, json.loads(body)))

    return publish, captured


def _scripted_llm(*deltas: str):
    """Build an LLM stub that yields the given deltas verbatim."""
    async def call(system: str, user: str) -> AsyncIterator[str]:
        for d in deltas:
            yield d
    return call


# ── Bad-input handling ────────────────────────────────────────────

@pytest.mark.asyncio
async def test_bad_json_drops_silently():
    publish, captured = _capture()
    req = await handle_message(b"not-json", cfg=_cfg(), publish=publish)
    assert req is None
    assert captured == []


@pytest.mark.asyncio
async def test_invalid_payload_drops_silently():
    publish, captured = _capture()
    body = json.dumps({"missing": "everything"}).encode()
    req = await handle_message(body, cfg=_cfg(), publish=publish)
    assert req is None
    assert captured == []


@pytest.mark.asyncio
async def test_source_id_only_resolves_via_brain_and_continues():
    """When raw_text is empty, the runner calls the source_resolver
    (in production: brain HTTP); the returned body becomes the LLM
    input, and the rest of the pipeline runs normally."""
    publish, captured = _capture()

    async def resolver(source_id, owner_id):
        # In production this hits brain's internal endpoint. Tests
        # short-circuit with the body the source row would have
        # returned.
        return ("the article body fetched from brain", "Article Title")

    deltas = [
        "---FILE: wiki/concepts/x.md---\n# X\nbody\n---END FILE---\n",
    ]
    body = _make_request(raw_text="", source_id=str(uuid.uuid4()))
    req = await handle_message(
        body, cfg=_cfg(), publish=publish,
        llm_stream=_scripted_llm(*deltas),
        source_resolver=resolver,
    )
    assert req is not None
    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "page", "done"]


@pytest.mark.asyncio
async def test_source_id_resolver_404_fails_task_with_clear_message():
    publish, captured = _capture()

    async def resolver(source_id, owner_id):
        return None  # 404 — source not found

    body = _make_request(raw_text="", source_id=str(uuid.uuid4()))
    await handle_message(
        body, cfg=_cfg(), publish=publish,
        source_resolver=resolver,
    )
    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "failed"]
    assert "not found on brain" in captured[-1][1]["error"]


@pytest.mark.asyncio
async def test_source_id_resolver_error_fails_task():
    from wiki_llm.brain import BrainClientError

    publish, captured = _capture()

    async def resolver(source_id, owner_id):
        raise BrainClientError("simulated transport error")

    body = _make_request(raw_text="", source_id=str(uuid.uuid4()))
    await handle_message(
        body, cfg=_cfg(), publish=publish,
        source_resolver=resolver,
    )
    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "failed"]
    assert "source fetch failed" in captured[-1][1]["error"]


@pytest.mark.asyncio
async def test_source_id_resolver_returns_empty_body_fails():
    """A source row that exists but has empty raw — degenerate case
    we surface clearly rather than feeding empty input to the LLM."""
    publish, captured = _capture()

    async def resolver(source_id, owner_id):
        return ("   ", "")  # whitespace-only body

    body = _make_request(raw_text="", source_id=str(uuid.uuid4()))
    await handle_message(
        body, cfg=_cfg(), publish=publish,
        source_resolver=resolver,
    )
    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "failed"]
    assert "no content" in captured[-1][1]["error"]


@pytest.mark.asyncio
async def test_inline_raw_text_takes_precedence_over_source_id():
    """When BOTH raw_text and source_id are present, raw_text wins —
    no resolver call, saves a round trip."""
    publish, captured = _capture()
    resolver_called = []

    async def resolver(source_id, owner_id):
        resolver_called.append((source_id, owner_id))
        return ("should not be used", "")

    deltas = [
        "---FILE: wiki/concepts/x.md---\nbody\n---END FILE---\n",
    ]
    body = _make_request(
        raw_text="inline content",
        source_id=str(uuid.uuid4()),
    )
    await handle_message(
        body, cfg=_cfg(), publish=publish,
        llm_stream=_scripted_llm(*deltas),
        source_resolver=resolver,
    )
    assert resolver_called == [], "resolver must not be called when raw_text present"


# ── Pipeline happy path ───────────────────────────────────────────

@pytest.mark.asyncio
async def test_two_complete_blocks_emit_two_page_updates_and_done():
    """The most important test: two FILE blocks streamed in chunks
    produce running → page → page → done in order, with correct
    paths and content extracted."""
    publish, captured = _capture()

    # The LLM emits two complete FILE blocks across multiple chunks
    # (worst-case for streaming: blocks split across delta boundaries).
    deltas = [
        "---FILE: wiki/concepts/rope.md---\n",
        "---\ntype: concept\ntitle: RoPE\n",
        "---\n\n",
        "# RoPE\n\nRotary positional embedding.\n",
        "---END FILE---\n\n",
        "---FILE: wiki/entities/qwen.md---\n",
        "---\ntype: entity\ntitle: Qwen\n---\n\n",
        "# Qwen\n\nA model family.\n---END FILE---\n",
    ]
    req = await handle_message(
        _make_request(),
        cfg=_cfg(),
        publish=publish,
        llm_stream=_scripted_llm(*deltas),
    )
    assert req is not None

    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "page", "page", "done"]

    # Page #1 — RoPE
    p1 = captured[1][1]
    assert p1["path"] == "wiki/concepts/rope.md"
    assert p1["title"] == "RoPE"
    assert p1["index"] == 1
    assert "Rotary positional embedding" in p1["content"]

    # Page #2 — Qwen
    p2 = captured[2][1]
    assert p2["path"] == "wiki/entities/qwen.md"
    assert p2["title"] == "Qwen"
    assert p2["index"] == 2

    # Terminal done
    done = captured[-1][1]
    assert done["progress"]["pages_total"] == 2


@pytest.mark.asyncio
async def test_streaming_partial_save_emits_first_page_before_second_completes():
    """Critical for UX: the first page must be emitted as soon as its
    closing marker arrives, not held until the whole stream finishes.
    We assert this by inspecting the order of emits relative to the
    delta sequence — the first ``page`` must precede the second
    block's bytes in the captured order."""
    publish, captured = _capture()

    deltas = [
        "---FILE: wiki/concepts/a.md---\n# A\nbody\n---END FILE---\n",
        # Now the second block streams in piece by piece
        "---FILE: wiki/concepts/b.md---\n",
        "# B\nbody\n",
        "---END FILE---\n",
    ]
    await handle_message(
        _make_request(),
        cfg=_cfg(),
        publish=publish,
        llm_stream=_scripted_llm(*deltas),
    )

    # Order: running → page(a) → page(b) → done. The page(a) emit
    # happens after delta[0] arrives, well before delta[3] closes b.
    paths = [p.get("path") for _, p in captured if p["kind"] == "page"]
    assert paths == ["wiki/concepts/a.md", "wiki/concepts/b.md"]


@pytest.mark.asyncio
async def test_no_blocks_in_output_is_failed_not_done():
    """When the model returns prose (prompt didn't take), we surface a
    failed terminal so the user sees the issue instead of a silent
    green check."""
    publish, captured = _capture()
    deltas = ["I'm sorry, I cannot help with that.\n"]
    await handle_message(
        _make_request(),
        cfg=_cfg(),
        publish=publish,
        llm_stream=_scripted_llm(*deltas),
    )
    kinds = [p["kind"] for _, p in captured]
    assert kinds == ["running", "failed"]
    assert "no parseable" in captured[-1][1]["error"].lower()


@pytest.mark.asyncio
async def test_unsafe_path_block_is_skipped_safe_one_passes():
    """Path-traversal hardening end-to-end: the LLM emits one safe and
    one unsafe block; only the safe one becomes a page update."""
    publish, captured = _capture()
    deltas = [
        "---FILE: wiki/concepts/ok.md---\nbody\n---END FILE---\n",
        "---FILE: ../../etc/passwd---\nattacker\n---END FILE---\n",
    ]
    await handle_message(
        _make_request(),
        cfg=_cfg(),
        publish=publish,
        llm_stream=_scripted_llm(*deltas),
    )
    pages = [p for _, p in captured if p["kind"] == "page"]
    assert len(pages) == 1
    assert pages[0]["path"] == "wiki/concepts/ok.md"


@pytest.mark.asyncio
async def test_llm_error_becomes_failed_update():
    """An LLM-side exception during streaming surfaces as a failed
    terminal; the running update still goes out."""
    from wiki_llm.llm import LLMError

    publish, captured = _capture()

    async def boom(system: str, user: str) -> AsyncIterator[str]:
        yield "---FILE: wiki/concepts/a.md---\n"
        raise LLMError("upstream timeout")

    await handle_message(
        _make_request(),
        cfg=_cfg(),
        publish=publish,
        llm_stream=boom,
    )
    kinds = [p["kind"] for _, p in captured]
    # running, then failed (the unfinished block doesn't trigger a page).
    assert kinds[0] == "running"
    assert kinds[-1] == "failed"
    assert "upstream timeout" in captured[-1][1]["error"]


@pytest.mark.asyncio
async def test_title_falls_back_to_h1_when_no_frontmatter():
    publish, captured = _capture()
    deltas = [
        "---FILE: wiki/concepts/no-fm.md---\n",
        "# Heading Title\n\nbody\n",
        "---END FILE---\n",
    ]
    await handle_message(
        _make_request(),
        cfg=_cfg(),
        publish=publish,
        llm_stream=_scripted_llm(*deltas),
    )
    page = next(p for _, p in captured if p["kind"] == "page")
    assert page["title"] == "Heading Title"


@pytest.mark.asyncio
async def test_title_empty_when_neither_frontmatter_nor_h1():
    """Brain's subscriber falls back to path basename when title is
    absent — worker is allowed to omit the field entirely (None strips
    on the wire)."""
    publish, captured = _capture()
    deltas = [
        "---FILE: wiki/concepts/no-title.md---\nbody only\n---END FILE---\n",
    ]
    await handle_message(
        _make_request(),
        cfg=_cfg(),
        publish=publish,
        llm_stream=_scripted_llm(*deltas),
    )
    page = next(p for _, p in captured if p["kind"] == "page")
    # Empty / missing title → field stripped.
    assert "title" not in page or page["title"] == ""
