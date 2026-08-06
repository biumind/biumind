"""Wire payload validation tests for IngestRequest + Update.

These exercise the boundary code that runs on every NATS message — bad
payloads from a misbehaving brain build must surface as ValueError, not
crash the worker loop.
"""

from __future__ import annotations

import uuid

import pytest

from wiki_llm.job import (
    IngestRequest,
    Update,
    KIND_PAGE,
    KIND_RUNNING,
)


def _uuid() -> str:
    return str(uuid.uuid4())


def test_ingest_request_minimal_with_raw_text():
    req = IngestRequest.from_payload({
        "task_id": _uuid(),
        "project_id": _uuid(),
        "owner_id": _uuid(),
        "raw_text": "hello",
    })
    assert req.raw_text == "hello"
    assert req.source_id is None
    assert req.title == ""


def test_ingest_request_with_source_id_no_text():
    sid = _uuid()
    req = IngestRequest.from_payload({
        "task_id": _uuid(),
        "project_id": _uuid(),
        "owner_id": _uuid(),
        "source_id": sid,
    })
    assert req.source_id == sid
    assert req.raw_text == ""


def test_ingest_request_rejects_missing_uuid():
    with pytest.raises(ValueError, match="task_id"):
        IngestRequest.from_payload({
            "project_id": _uuid(),
            "owner_id": _uuid(),
            "raw_text": "x",
        })


def test_ingest_request_rejects_bad_uuid():
    with pytest.raises(ValueError, match="UUID"):
        IngestRequest.from_payload({
            "task_id": "not-a-uuid",
            "project_id": _uuid(),
            "owner_id": _uuid(),
            "raw_text": "x",
        })


def test_ingest_request_rejects_no_input():
    with pytest.raises(ValueError, match="source_id or raw_text"):
        IngestRequest.from_payload({
            "task_id": _uuid(),
            "project_id": _uuid(),
            "owner_id": _uuid(),
        })


def test_ingest_request_rejects_non_dict():
    with pytest.raises(ValueError):
        IngestRequest.from_payload([1, 2, 3])


def test_update_strips_none_fields():
    u = Update(task_id=_uuid(), kind=KIND_RUNNING).to_payload()
    # Only required fields survive.
    assert set(u.keys()) == {"task_id", "kind"}


def test_update_keeps_present_fields():
    u = Update(
        task_id=_uuid(), kind=KIND_PAGE,
        path="wiki/concepts/rope.md",
        title="RoPE", content="# RoPE\nbody", index=3,
    ).to_payload()
    assert u["kind"] == KIND_PAGE
    assert u["path"] == "wiki/concepts/rope.md"
    assert u["index"] == 3
    assert u["content"].startswith("# RoPE")
    # Untouched fields stay omitted.
    assert "error" not in u
