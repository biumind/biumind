"""NATS-driven worker loop.

One queue subscription per worker process — multiple replicas share the
queue group ``brain-ingest-py`` so each binary job lands on exactly one
extractor.
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import Awaitable, Callable, Optional

from .config import Config
from .extractors import ExtractorContext, ExtractorError, ExtractorUnavailable, get
from .job import BinaryJob, TextJob


logger = logging.getLogger("biumind.ingest")

# Type alias for the publish callback so tests can pass a stub.
Publisher = Callable[[str, bytes], Awaitable[None]]


async def handle_message(
    raw: bytes,
    *,
    cfg: Config,
    publish: Publisher,
    extractor_factory: Callable[[str], Callable] = get,
) -> Optional[TextJob]:
    """Decode a NATS message, extract text, and publish a TextJob.

    Returns the TextJob that was published (None when extraction fails
    or input is malformed). Tests assert on the return value; production
    just calls this with a real NATS publisher closure.
    """
    try:
        payload = json.loads(raw.decode("utf-8"))
    except json.JSONDecodeError as e:
        logger.warning("ingest: bad json: %s", e)
        return None
    try:
        job = BinaryJob.from_payload(payload)
    except ValueError as e:
        logger.warning("ingest: invalid job: %s", e)
        return None

    logger.debug(
        "ingest: job received source_id=%s kind=%s bytes=%d",
        job.source_id, job.kind, len(job.data),
    )

    ctx = ExtractorContext(
        whisper_model=cfg.whisper_model,
        whisper_device=cfg.whisper_device,
        tesseract_lang=cfg.tesseract_lang,
    )

    try:
        extract = extractor_factory(job.kind)
    except ExtractorError as e:
        logger.warning("ingest: %s", e)
        return None

    try:
        # Extraction is CPU-bound — push to a thread so the asyncio
        # event loop keeps NATS keep-alives flowing while OCR / Whisper
        # are crunching.
        extract_start = asyncio.get_event_loop().time()
        text = await asyncio.wait_for(
            asyncio.to_thread(extract, job.data, ctx),
            timeout=cfg.timeout_s,
        )
        logger.debug(
            "ingest: extract done source_id=%s kind=%s chars=%d latency_ms=%d",
            job.source_id, job.kind, len(text),
            int((asyncio.get_event_loop().time() - extract_start) * 1000),
        )
    except ExtractorUnavailable as e:
        logger.error("ingest: extractor for %s unavailable: %s", job.kind, e)
        return None
    except ExtractorError as e:
        logger.warning("ingest: %s extraction failed for source_id=%s: %s",
                       job.kind, job.source_id, e)
        return None
    except asyncio.TimeoutError:
        logger.warning("ingest: %s extraction timed out for source_id=%s",
                       job.kind, job.source_id)
        return None
    except Exception:  # noqa: BLE001 — last-resort log
        logger.exception("ingest: unexpected error extracting source_id=%s",
                         job.source_id)
        return None

    text = text.strip()
    if not text:
        logger.warning("ingest: empty extraction for source_id=%s", job.source_id)
        return None

    text_job = TextJob.from_extracted(job, text)
    body = json.dumps(text_job.to_payload(), ensure_ascii=False).encode("utf-8")
    await publish(cfg.text_subject, body)
    logger.info("ingest: handed off source_id=%s kind=%s chars=%d → %s",
                job.source_id, job.kind, len(text), cfg.text_subject)
    return text_job


async def run(cfg: Config) -> None:
    """Top-level entry point. Connects to NATS, subscribes, blocks
    until cancelled. Imported lazily so tests don't need ``nats-py``."""
    import nats  # type: ignore

    nc = await nats.connect(cfg.nats_url, name="biumind-ingest-py")
    logger.info("ingest worker connected nats=%s sub=%s queue=%s",
                cfg.nats_url, cfg.binary_subject, cfg.queue_group)

    async def _publish(subject: str, body: bytes) -> None:
        await nc.publish(subject, body)

    async def _handle(msg) -> None:  # noqa: ANN001 — nats msg is dynamic
        try:
            await handle_message(msg.data, cfg=cfg, publish=_publish)
        except Exception:  # noqa: BLE001
            logger.exception("ingest: handler crashed")

    await nc.subscribe(cfg.binary_subject, queue=cfg.queue_group, cb=_handle)
    try:
        # Block until cancelled.
        stop = asyncio.Event()
        await stop.wait()
    finally:
        await nc.drain()
