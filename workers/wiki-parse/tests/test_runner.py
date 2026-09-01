"""handle_message 的信封解包测试。

brain 的 BusPublisher 把业务 payload 包进 {topic, kind, payload} 信封
（services/brain/internal/publisher/bus.go），而 parse-queue rescan 路径
直接给业务层。本测试用哨兵异常区分「解包成功并进 handle_job」与
「没解包导致 from_payload 失败返回 None」。
"""

from __future__ import annotations

import json
import uuid

import pytest

from wiki_parse.config import Config
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
