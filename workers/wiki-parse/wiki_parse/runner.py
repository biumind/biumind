"""wiki-parse worker loop.

两种触发并行（双保险）：
- NATS subscribe（upload 入库即时触发，主路径）
- tick loop（parse_queue_interval_s 扫 queued/error 兜底，防 NATS 漏发 +
  worker 宕机期间积压 + dev NoopBus 场景）

每 job 流水：
  1. blob-presign → httpx stream-download（带 max_bytes 上限）
  2. parser.extract → extracted_text
  3. content_hash = sha256(extracted_text)
  4. POST parse-result done（brain 同步做项目内 source dedup → review_items）
失败 → POST parse-result error（parse_error 落库，retries++，<3 下个 tick 重试）

竞态说明：NATS queue group 单 consumer 派发不双派；tick rescan 与 NATS 理论
可能撞同一 queued 行（NATS handler 收到 → UPDATE done 之间）。UpdateParseStatus
是幂等 UPDATE（同文件同 parser 输出同 hash，最后写赢），dedupe_key UNIQUE 防双
review_item —— 幂等可接受。深度防重入（起手 UPDATE processing + CAS）排期。
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import logging
from typing import Awaitable, Callable, Optional, Tuple

from .brain_client import (
    BrainClientError, BrainConfig,
    download_blob, get_blob_presign, get_parse_queue, post_parse_result,
)
from .config import Config
from .job import ParseJob
from .parser import ParseError, extract


logger = logging.getLogger("biumind.wiki_parse")


# 测试注入点：(source_id, owner_id) → file bytes。production 用默认 presign+download。
BlobFetcher = Callable[[str, str], Awaitable[bytes]]


async def handle_job(
    job: ParseJob,
    *,
    cfg: Config,
    fetch_blob: Optional[BlobFetcher] = None,
) -> Tuple[str, Optional[str]]:
    """处理单个 parse job。返回 (parse_status, error_or_None)。

    成功路径：done + extracted_text + content_hash 回写（brain 同步 dedup）。
    任一步失败：返回 error + message，caller 再 post error 回写（retries++）。
    """
    fetcher = fetch_blob if fetch_blob is not None else _default_fetcher(cfg)
    brain_cfg = BrainConfig(
        base_url=cfg.brain_url, internal_token=cfg.internal_token,
    )

    # 1. 拉文件
    try:
        data = await fetcher(job.source_id, job.owner_id)
    except BrainClientError as e:
        logger.warning("wiki_parse: fetch blob failed source=%s: %s",
                       job.source_id, e)
        return "error", f"fetch blob failed: {e}"

    # 2. 提取
    try:
        text = extract(data, mime=job.mime, filename=job.filename)
    except ParseError as e:
        logger.info("wiki_parse: extract failed source=%s: %s", job.source_id, e)
        return "error", str(e)
    except Exception as e:  # noqa: BLE001 — parser 未预期异常兜底
        logger.exception("wiki_parse: unexpected extract error source=%s",
                         job.source_id)
        return "error", f"unexpected: {e}"

    if not text.strip():
        return "error", "extracted text empty"

    # 3. hash + 回写 done（brain 同步做 dedup）
    content_hash = hashlib.sha256(text.encode("utf-8")).hexdigest()
    try:
        await post_parse_result(
            brain_cfg, source_id=job.source_id, owner_id=job.owner_id,
            extracted_text=text, content_hash=content_hash,
            parse_status="done",
        )
    except BrainClientError as e:
        logger.warning("wiki_parse: post done failed source=%s: %s",
                       job.source_id, e)
        return "error", f"post done failed: {e}"

    logger.info("wiki_parse: done source=%s chars=%d hash=%s",
                job.source_id, len(text), content_hash[:12])
    return "done", None


async def handle_message(
    raw: bytes,
    *,
    cfg: Config,
    fetch_blob: Optional[BlobFetcher] = None,
) -> Optional[ParseJob]:
    """NATS message → ParseJob → handle_job。失败时再 post error 回写。"""
    try:
        payload = json.loads(raw.decode("utf-8"))
    except json.JSONDecodeError as e:
        logger.warning("wiki_parse: bad json: %s", e)
        return None
    try:
        job = ParseJob.from_payload(payload)
    except ValueError as e:
        logger.warning("wiki_parse: invalid job: %s", e)
        return None

    status, err = await handle_job(job, cfg=cfg, fetch_blob=fetch_blob)
    if status == "error" and err:
        brain_cfg = BrainConfig(
            base_url=cfg.brain_url, internal_token=cfg.internal_token,
        )
        try:
            await post_parse_result(
                brain_cfg, source_id=job.source_id, owner_id=job.owner_id,
                extracted_text="", content_hash="",
                parse_status="error", parse_error=err,
            )
        except BrainClientError as e:
            logger.warning("wiki_parse: post error failed source=%s: %s",
                           job.source_id, e)
    return job


def _default_fetcher(cfg: Config) -> BlobFetcher:
    brain_cfg = BrainConfig(
        base_url=cfg.brain_url, internal_token=cfg.internal_token,
    )

    async def _fetch(source_id: str, owner_id: str) -> bytes:
        presign = await get_blob_presign(
            brain_cfg, source_id=source_id, owner_id=owner_id,
        )
        if presign is None:
            raise BrainClientError(f"source {source_id} not found / no file")
        return await download_blob(presign.url, max_bytes=cfg.max_file_bytes)

    return _fetch


async def _tick_loop(
    cfg: Config, fetch_blob: Optional[BlobFetcher] = None,
) -> None:
    """启动即跑一次（接 queued 积压），之后按 interval 循环。"""
    brain_cfg = BrainConfig(
        base_url=cfg.brain_url, internal_token=cfg.internal_token,
    )
    await asyncio.sleep(2)  # 让 NATS subscribe 先就绪
    while True:
        try:
            items = await get_parse_queue(brain_cfg)
            if items:
                logger.info("wiki_parse: tick rescan %d queued", len(items))
            for it in items:
                job = it.as_job()
                try:
                    status, err = await handle_job(
                        job, cfg=cfg, fetch_blob=fetch_blob,
                    )
                    if status == "error" and err:
                        # tick 路径也补 error 回写（与 NATS 路径一致）
                        try:
                            await post_parse_result(
                                brain_cfg, source_id=job.source_id,
                                owner_id=job.owner_id,
                                extracted_text="", content_hash="",
                                parse_status="error", parse_error=err,
                            )
                        except BrainClientError as e:
                            logger.warning(
                                "wiki_parse: tick post error failed source=%s: %s",
                                job.source_id, e,
                            )
                except Exception:  # noqa: BLE001 — 单 job 崩不杀 loop
                    logger.exception(
                        "wiki_parse: tick job crashed source=%s", job.source_id,
                    )
        except BrainClientError as e:
            logger.warning("wiki_parse: tick queue fetch failed: %s", e)
        except Exception:  # noqa: BLE001 — loop 兜底
            logger.exception("wiki_parse: tick loop unexpected")
        await asyncio.sleep(cfg.parse_queue_interval_s)


async def run(
    cfg: Config, *, fetch_blob: Optional[BlobFetcher] = None,
) -> None:
    """Top-level：NATS subscribe + tick loop 并行，阻塞到取消。"""
    import nats  # type: ignore

    if not cfg.brain_url or not cfg.internal_token:
        raise RuntimeError(
            "BIUMIND_BRAIN_URL + BIUMIND_INTERNAL_TOKEN required — "
            "wiki-parse worker talks to brain internal endpoints"
        )

    nc = await nats.connect(cfg.nats_url, name="biumind-wiki-parse")
    logger.info(
        "wiki_parse worker connected nats=%s sub=%s queue=%s interval=%ss",
        cfg.nats_url, cfg.request_subject, cfg.queue_group,
        cfg.parse_queue_interval_s,
    )

    async def _handle(msg) -> None:  # noqa: ANN001 — nats msg dynamic
        try:
            await handle_message(msg.data, cfg=cfg, fetch_blob=fetch_blob)
        except Exception:  # noqa: BLE001 — handler 崩不杀 consumer
            logger.exception("wiki_parse: handler crashed")

    await nc.subscribe(cfg.request_subject, queue=cfg.queue_group, cb=_handle)

    tick_task = asyncio.create_task(_tick_loop(cfg, fetch_blob=fetch_blob))

    try:
        stop = asyncio.Event()
        await stop.wait()
    finally:
        tick_task.cancel()
        await nc.drain()
