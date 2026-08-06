"""NATS-driven AIGC worker loop.

职责:
  1. 订阅 aigc.task.submit (queue group, 多副本竞争消费)
  2. 路由到 providers/<code> Executor
  3. 调 submit → 周期 poll → 完成时 publish aigc.task.update (含 outputs)
  4. 失败 → publish aigc.task.update(status=failed) + (P3+) 自动退款

P3 阶段简化: 完成态的 output_urls 直接当 storage_url 传给 services/aigc
(P3-5 storage persist 上线后改为 MinIO 转存的 cas:<sha> URL).
"""

from __future__ import annotations

import asyncio
import dataclasses
import hashlib
import json
import logging
from typing import Awaitable, Callable, Optional

from .config import Config
from .event import OutputEntry, SubmitTask, TaskUpdate
from .providers import Executor, ProviderError, ProviderUnavailable, get as get_provider


logger = logging.getLogger("biumind.aigc")

# Type alias for the publish callback so tests can pass a stub.
Publisher = Callable[[str, bytes], Awaitable[None]]


PersistFn = Callable[[str, str], Awaitable["object"]]
"""(url, kind) → PersistedOutput 协议. 让测试用 stub 替代真 MinIO 调用."""


async def handle_submit(
    raw: bytes,
    *,
    cfg: Config,
    publish: Publisher,
    provider_factory: Callable[[str, Config], Executor] = get_provider,
    persist_fn: Optional[PersistFn] = None,
) -> Optional[TaskUpdate]:
    """处理一条 aigc.task.submit 消息.

    返回最终发出的 TaskUpdate (None 表示消息无法解码; 此时不发任何事件).
    生产路径调用方传入 nats publish closure; 测试用 stub.
    """
    try:
        task = SubmitTask.from_json(raw)
    except (KeyError, ValueError) as e:
        logger.warning("aigc: bad submit msg: %s", e)
        return None

    logger.info(
        "aigc: submit received task_id=%s type=%s model=%s provider=%s key=%s",
        task.task_id, task.type, task.model_code, task.provider_code,
        # 永远不打全 key, 只 last4 hint (P4.S3.2 安全要求).
        task.credential_last4 or ("env" if not task.has_payload_credential() else "**"),
    )

    # P4.S3.2: 凭证优先级 — payload 注入的明文 (model-relay vault decrypt)
    # 优先于 env 兜底. has_payload_credential() 为真时构造一份 task-specific
    # cfg, 让 dashscope/volcengine provider 拿到 task 里的 key.
    cfg_for_task = cfg
    if task.has_payload_credential():
        cfg_for_task = dataclasses.replace(
            cfg,
            dashscope_api_key=task.credential_api_key,
            volcengine_ark_api_key=task.credential_api_key,
        )

    # 路由到 provider
    try:
        executor = provider_factory(task.provider_code, cfg_for_task)
    except ProviderUnavailable as e:
        return await _emit_failed(publish, cfg, task, "PROVIDER_UNAVAILABLE", str(e))

    # 1. submit → 上游
    submit_start = asyncio.get_event_loop().time()
    try:
        external_id = await executor.submit(task)
    except ProviderUnavailable as e:
        return await _emit_failed(publish, cfg, task, "PROVIDER_UNAVAILABLE", str(e))
    except ProviderError as e:
        return await _emit_failed(publish, cfg, task, "UPSTREAM_SUBMIT", str(e))
    except Exception as e:  # pragma: no cover — 兜底防 worker 死循环
        logger.exception("aigc: unexpected submit error")
        return await _emit_failed(publish, cfg, task, "INTERNAL", str(e))
    logger.debug(
        "aigc: upstream submitted task_id=%s external_id=%s latency_ms=%d",
        task.task_id, external_id,
        int((asyncio.get_event_loop().time() - submit_start) * 1000),
    )

    # 2. queued → published
    upd = TaskUpdate(task_id=task.task_id, status="queued", external_task_id=external_id)
    await publish(cfg.update_subject, upd.to_json())

    # 3. 轮询 → terminal
    deadline = asyncio.get_event_loop().time() + cfg.timeout_s
    last_progress = 0
    while True:
        if asyncio.get_event_loop().time() > deadline:
            return await _emit_failed(publish, cfg, task, "TIMEOUT",
                                      f"exceeded {cfg.timeout_s}s")

        try:
            outcome = await executor.poll(external_id)
        except ProviderError as e:
            return await _emit_failed(publish, cfg, task, "UPSTREAM_POLL", str(e))
        except Exception as e:
            logger.exception("aigc: unexpected poll error")
            return await _emit_failed(publish, cfg, task, "INTERNAL", str(e))

        logger.debug(
            "aigc: poll task_id=%s external_id=%s status=%s progress=%d",
            task.task_id, external_id, outcome.status, outcome.progress,
        )

        # progress 单调递增 (避免回退)
        if outcome.progress > last_progress:
            last_progress = outcome.progress

        if outcome.status == "running":
            running = TaskUpdate(
                task_id=task.task_id, status="running",
                progress=last_progress, external_task_id=external_id,
            )
            await publish(cfg.update_subject, running.to_json())
            await asyncio.sleep(cfg.poll_interval_s)
            continue

        if outcome.status == "blocked":
            return await _emit_failed(publish, cfg, task, "MODERATION_BLOCKED",
                                      outcome.error_message or "content blocked",
                                      external_id, status="blocked")

        if outcome.status == "failed":
            return await _emit_failed(publish, cfg, task,
                                      outcome.error_code or "UPSTREAM_FAILED",
                                      outcome.error_message or "upstream failed",
                                      external_id)

        # hotparse: 结构化产物 (文案/分镜, 无媒体 URL) → 直接构造一条
        # kind="hotparse" output, metadata 内联拆解结果, 跳过 MinIO 转存。
        if outcome.structured is not None:
            outputs = _build_hotparse_outputs(outcome.structured)
        else:
            # completed → 转存厂商 URL 到 MinIO 拿 cas:<sha>
            try:
                outputs = await _build_persisted_outputs(outcome, persist_fn)
            except Exception as e:
                logger.exception("aigc: persist failed task_id=%s", task.task_id)
                return await _emit_failed(publish, cfg, task, "PERSIST_FAILED", str(e),
                                          external_id)
        upd = TaskUpdate(
            task_id=task.task_id, status="completed", progress=100,
            external_task_id=external_id, outputs=outputs,
        )
        await publish(cfg.update_subject, upd.to_json())
        logger.info("aigc: completed task_id=%s outputs=%d", task.task_id, len(outputs))
        return upd


async def _emit_failed(
    publish: Publisher,
    cfg: Config,
    task: SubmitTask,
    code: str,
    message: str,
    external_id: str = "",
    status: str = "failed",
) -> TaskUpdate:
    """发 failed/blocked 事件并尝试退款.

    退款责任: P3 阶段简化为同步等服务端 (orchestrator 收到 refunded_credits>0
    时才记 task.refunded_credits). 真的 billing.Refund 调用要等 P5 把 worker
    的 billing client 接进来; 现在 worker 把 cost_credits 当 refunded_credits
    报回, 让 services/aigc orchestrator + Flutter UI 都能正确显示「已退还 N 积分」.
    实际钱在 identity.user_credits 里要由谁退是 P5 决议项.
    """
    upd = TaskUpdate(
        task_id=task.task_id, status=status,
        error_code=code, error_message=message,
        external_task_id=external_id,
        refunded_credits=task.cost_credits,
    )
    await publish(cfg.update_subject, upd.to_json())
    logger.warning("aigc: task %s failed: %s — %s", task.task_id, code, message)
    return upd


def _build_hotparse_outputs(structured: dict) -> list[OutputEntry]:
    """爆款解析结构化结果 → 一条 kind="hotparse" 的 OutputEntry。

    无媒体文件,sha256 取结果 JSON 的 sha(满足 task_outputs.sha256 非空 +
    幂等约束),storage_url/key 留空。拆解结果内联进 metadata,由 services/aigc
    orchestrator 透传落 task_outputs.metadata jsonb,客户端按 kind 解析渲染。
    """
    canonical = json.dumps(structured, ensure_ascii=False, sort_keys=True).encode("utf-8")
    sha = hashlib.sha256(canonical).hexdigest()
    return [
        OutputEntry(
            idx=0,
            kind="hotparse",
            sha256=sha,
            storage_url="",
            storage_key="",
            mime_type="application/json",
            metadata=structured,
        )
    ]


async def _build_persisted_outputs(
    outcome, persist_fn: Optional[PersistFn],
) -> list[OutputEntry]:
    """把 outcome 的厂商 URL 转存到 MinIO, 构造 OutputEntry.

    persist_fn=None 时 fallback 到旧行为 (直接用厂商 URL, sha 占位 "pending-N").
    生产路径必须传入真 Persister.persist_url. 视频 outcome.output_meta[i].cover_url
    会被额外转存一次到 derivatives bucket, 并在最终 OutputEntry 里用 cover_sha 字段
    指向那条 cover output (可选: 也可以单独再发一个 idx=N+1 kind=cover 的 entry).
    """
    out: list[OutputEntry] = []
    for i, url in enumerate(outcome.output_urls):
        meta = outcome.output_meta[i] if i < len(outcome.output_meta) else {}
        kind = meta.get("kind", "image")

        if persist_fn is None:
            # 兜底 (单测 / dev 无 storage extra 时的旧行为)
            out.append(OutputEntry(
                idx=i, kind=kind,
                sha256=meta.get("sha256", f"pending-{i}"),
                storage_url=url,
                storage_key=meta.get("storage_key", "pending"),
                mime_type=meta.get("mime_type", ""),
                file_size=meta.get("file_size", 0),
                width=meta.get("width", 0),
                height=meta.get("height", 0),
                duration_ms=meta.get("duration_ms", 0),
            ))
            continue

        # 主路径: 转存厂商 URL → cas:<sha>
        # Persister.persist_url(self, url, *, kind) 的 kind 是 keyword-only,
        # 必须按关键字传 (位置传会 TypeError)。
        po = await persist_fn(url, kind=kind)
        cover_sha = getattr(po, "cover_sha", "") or ""
        # 视频 outcome 可能在 meta 里多带一个 cover_url 让我们独立转存 (优先于 ffmpeg 抽帧)
        meta_cover = meta.get("cover_url")
        if kind == "video" and meta_cover and not cover_sha:
            try:
                cover_po = await persist_fn(meta_cover, kind="cover")
                cover_sha = cover_po.sha256
            except Exception as e:
                logger.warning("persist cover_url failed: %s", e)

        out.append(OutputEntry(
            idx=i, kind=kind,
            sha256=po.sha256,
            storage_url=po.storage_url,
            storage_key=po.storage_key,
            cover_sha=cover_sha,
            mime_type=po.mime_type,
            file_size=po.file_size,
            width=po.width,
            height=po.height,
            duration_ms=po.duration_ms,
            blurhash=getattr(po, "blurhash", "") or "",
        ))
    return out


# ─── NATS 主循环 ──────────────────────────────────────


async def run(cfg: Config) -> None:
    """启动 worker: 连 NATS, 订阅 aigc.task.submit, 阻塞直到 SIGINT."""
    import nats  # 延迟 import 让单测不强依赖 NATS 客户端

    nc = await nats.connect(cfg.nats_url, name="biumind-aigc-worker")
    logger.info("aigc: connected to NATS %s subject=%s queue=%s",
                cfg.nats_url, cfg.submit_subject, cfg.queue_group)

    # 装配 storage Persister (S3 配置齐全时启用; 否则走老兜底, 厂商 URL 直传)
    persist_fn = None
    if cfg.s3_endpoint and cfg.s3_access_key and cfg.s3_secret_key:
        try:
            from .storage import Persister
            persister = Persister(
                s3_endpoint=cfg.s3_endpoint,
                s3_access_key=cfg.s3_access_key,
                s3_secret_key=cfg.s3_secret_key,
                s3_region=cfg.s3_region,
                bucket_outputs=cfg.bucket_outputs,
                bucket_derivatives=cfg.bucket_derivatives,
            )
            persist_fn = persister.persist_url
            logger.info("aigc: persister enabled endpoint=%s outputs=%s",
                        cfg.s3_endpoint, cfg.bucket_outputs)
        except ImportError as e:
            logger.warning("aigc: persister disabled (import error): %s", e)
    else:
        logger.warning("aigc: persister disabled (S3 config missing) — outputs.storage_url 将含厂商临时 URL")

    async def _publish(subject: str, body: bytes) -> None:
        await nc.publish(subject, body)

    async def _on_msg(msg) -> None:
        # 每条消息独立 task 处理; 异常不要拖垮订阅
        try:
            await handle_submit(msg.data, cfg=cfg, publish=_publish,
                                persist_fn=persist_fn)
        except Exception:  # pragma: no cover
            logger.exception("aigc: handle_submit crashed")

    sub = await nc.subscribe(
        cfg.submit_subject, queue=cfg.queue_group, cb=_on_msg,
    )

    # 阻塞至 cancel
    try:
        await asyncio.Event().wait()
    finally:
        await sub.drain()
        await nc.drain()
        logger.info("aigc: drained")
