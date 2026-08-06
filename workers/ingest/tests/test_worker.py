"""End-to-end worker handle_message tests using a stub extractor and
in-memory publisher — no NATS or external binaries required."""

from __future__ import annotations

import asyncio
import base64
import json
import unittest

from biumind_ingest.config import Config
from biumind_ingest.extractors import ExtractorContext, ExtractorError, ExtractorUnavailable
from biumind_ingest.worker import handle_message


def _cfg() -> Config:
    return Config(
        nats_url="nats://localhost:4222",
        env="test",
        queue_group="q",
        timeout_s=2,
        whisper_model="base",
        whisper_device="cpu",
        tesseract_lang="eng",
    )


def _binary_msg(**overrides) -> bytes:
    payload = {
        "source_id": "src",
        "project_id": "proj",
        "user_id": "user",
        "kind": "pdf",
        "title": "Sample",
        "url": "https://x/y.pdf",
        "data_b64": base64.b64encode(b"PDF DATA").decode("ascii"),
    }
    payload.update(overrides)
    return json.dumps(payload).encode("utf-8")


class WorkerHandleTests(unittest.IsolatedAsyncioTestCase):
    async def test_happy_path_publishes_text_job(self):
        published: list[tuple[str, bytes]] = []

        async def publish(subject: str, body: bytes) -> None:
            published.append((subject, body))

        def stub_extractor(_kind: str):
            def _extract(data: bytes, _ctx: ExtractorContext) -> str:
                return f"text from {len(data)} bytes"
            return _extract

        cfg = _cfg()
        result = await handle_message(_binary_msg(),
                                      cfg=cfg,
                                      publish=publish,
                                      extractor_factory=stub_extractor)
        self.assertIsNotNone(result)
        self.assertEqual(len(published), 1)
        subject, body = published[0]
        self.assertEqual(subject, cfg.text_subject)
        decoded = json.loads(body)
        # Must match Go ingestbus.Job shape.
        self.assertEqual(decoded["kind"], "plain")
        self.assertEqual(decoded["title"], "Sample")
        self.assertEqual(decoded["content"], "text from 8 bytes")
        self.assertEqual(decoded["source_id"], "src")

    async def test_unsupported_kind_swallowed(self):
        published: list[tuple[str, bytes]] = []

        async def publish(subject, body):
            published.append((subject, body))

        result = await handle_message(_binary_msg(kind="docx"),
                                      cfg=_cfg(),
                                      publish=publish,
                                      extractor_factory=lambda _: None)
        self.assertIsNone(result)
        self.assertEqual(published, [])

    async def test_extractor_error_does_not_publish(self):
        published: list[tuple[str, bytes]] = []

        async def publish(subject, body):
            published.append((subject, body))

        def boom_extractor(_kind: str):
            def _e(data, ctx):
                raise ExtractorError("corrupt")
            return _e

        result = await handle_message(_binary_msg(),
                                      cfg=_cfg(),
                                      publish=publish,
                                      extractor_factory=boom_extractor)
        self.assertIsNone(result)
        self.assertEqual(published, [])

    async def test_extractor_unavailable_does_not_crash(self):
        published: list[tuple[str, bytes]] = []

        async def publish(subject, body):
            published.append((subject, body))

        def missing_extractor(_kind: str):
            def _e(data, ctx):
                raise ExtractorUnavailable("install [pdf]")
            return _e

        result = await handle_message(_binary_msg(),
                                      cfg=_cfg(),
                                      publish=publish,
                                      extractor_factory=missing_extractor)
        self.assertIsNone(result)

    async def test_extractor_timeout(self):
        async def publish(_s, _b): ...

        def slow_extractor(_kind: str):
            def _e(data, ctx):
                import time
                time.sleep(5)  # > timeout_s=2
                return "never"
            return _e

        cfg = _cfg()
        result = await asyncio.wait_for(
            handle_message(_binary_msg(),
                           cfg=cfg,
                           publish=publish,
                           extractor_factory=slow_extractor),
            timeout=cfg.timeout_s + 1,
        )
        self.assertIsNone(result)

    async def test_empty_extraction_swallowed(self):
        async def publish(_s, _b): ...

        def empty_extractor(_kind: str):
            def _e(data, ctx):
                return "   \n  "
            return _e

        result = await handle_message(_binary_msg(),
                                      cfg=_cfg(),
                                      publish=publish,
                                      extractor_factory=empty_extractor)
        self.assertIsNone(result)

    async def test_bad_json_swallowed(self):
        async def publish(_s, _b): ...
        result = await handle_message(b"not json",
                                      cfg=_cfg(),
                                      publish=publish,
                                      extractor_factory=lambda _: None)
        self.assertIsNone(result)


if __name__ == "__main__":
    unittest.main()
