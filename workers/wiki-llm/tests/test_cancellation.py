"""CancelRegistry TTL semantics + cancel-broadcast wire decoding."""

from __future__ import annotations

import json

from wiki_llm.cancellation import CancelRegistry, parse_cancel_task_id


def test_unknown_task_is_not_cancelled():
    assert not CancelRegistry().is_cancelled("nope")


def test_added_task_is_cancelled():
    reg = CancelRegistry()
    reg.add("t1")
    assert reg.is_cancelled("t1")
    assert not reg.is_cancelled("t2")


def test_ttl_expiry():
    now = [100.0]
    reg = CancelRegistry(ttl_s=10.0, clock=lambda: now[0])
    reg.add("t1")
    now[0] = 105.0
    assert reg.is_cancelled("t1")
    now[0] = 200.0
    assert not reg.is_cancelled("t1")


def test_parse_bare_payload():
    body = json.dumps({"task_id": "abc"}).encode()
    assert parse_cancel_task_id(body) == "abc"


def test_parse_envelope_payload():
    body = json.dumps({
        "topic": "wiki.ingest", "kind": "cancel",
        "payload": {"task_id": "abc"},
    }).encode()
    assert parse_cancel_task_id(body) == "abc"


def test_parse_junk_returns_none():
    assert parse_cancel_task_id(b"not-json") is None
    assert parse_cancel_task_id(json.dumps({"nope": 1}).encode()) is None
    assert parse_cancel_task_id(json.dumps({"task_id": ""}).encode()) is None
