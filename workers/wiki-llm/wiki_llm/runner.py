"""NATS-driven worker loop with full LLM ingest pipeline.

Per task:

  1. ``running`` update — flips brain task to running
  2. (two-stage only) fetch the project's ingest context from brain
     (purpose / schema / page index), then run stage-1 analysis:
     a one-shot LLM call producing entities / concepts / relations /
     conflicts / suggested page split. Stage-1 failure = task failed
     (quality gate — no degraded bare stage-2 run).
  3. Stream stage 2 (FILE-block generation, prompt carries the stage-1
     analysis + wiki context) via hub; accumulate text into a buffer
  4. After each delta, re-run ``parse_file_blocks`` on the cumulative
     buffer; emit one ``page`` update per NEW closed block
  5. Stream ends → ``done`` update (or ``failed`` on exception, or
     ``cancelled`` if the runner observed the task's cancel signal)

The two-stage shape (P2 #17) is on by default;
``BIUMIND_WIKI_LLM_TWO_STAGE=0`` falls back to the legacy single-stage
pipeline (steps 2 skipped) for prod A/B comparison.

Streaming partial-save trick: the parser is pure + idempotent, so on
each call we simply diff "blocks now" vs "blocks emitted last call"
and emit whichever appeared. This is O(n²) in chunk count but n is
small (typical CoT response: 5-15 blocks, ~50 chunks). When that
stops being true we'll switch to a true incremental parser.

Cancellation: brain's cancel API sets ``cancel_requested_at`` on the
task row AND publishes a broadcast on ``biumind.<env>.brain.wiki.
ingest.cancel``. ``run`` feeds that subject into a ``CancelRegistry``
which this pipeline consults at two points: on pickup (skip queued
tasks already cancelled, no LLM call burned) and at every stream chunk
boundary (abort in-flight streams, emit ``cancelled``; pages already
emitted stay saved per streaming partial-save semantics). The
fire-and-forget hole (no worker connected when the broadcast fired) is
closed brain-side by the reaper's cancel sweep.
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import Awaitable, Callable, Iterable, Optional, Tuple

from .brain import (
    BrainClientError,
    BrainConfig,
    IngestContext,
    fetch_ingest_context,
    fetch_source,
)
from .cancellation import CancelRegistry, parse_cancel_task_id
from .config import BUILTIN_FALLBACK_MODEL, Config
from .default_model import DefaultModelResolver
from .domain.frontmatter import parse_frontmatter
from .domain.ingest_parse import FileBlock, parse_file_blocks
from .domain.sanitize import sanitize_ingested_content
from .job import (
    IngestRequest,
    Update,
    KIND_RUNNING,
    KIND_PAGE,
    KIND_DONE,
    KIND_FAILED,
    KIND_CANCELLED,
)
from .llm import LLMConfig, LLMError, complete_messages, stream_messages
from .preference_model import PreferenceModelResolver


logger = logging.getLogger("biumind.wiki_llm")


# Type alias for the publish callback so tests can pass a stub.
Publisher = Callable[[str, bytes], Awaitable[None]]

# Type alias for the LLM streaming callable so tests can swap a fake.
# Receives (system, user) → AsyncIterator[str] of text deltas.
LLMStreamer = Callable[[str, str], "AsyncStr"]
AsyncStr = Iterable[str]  # actually AsyncIterator[str]; named loosely for type-hints

# Type alias for the one-shot (non-streaming) LLM callable used by the
# stage-1 analysis of the two-stage pipeline. Receives (system, user)
# → full response text. Errors raise LLMError.
LLMCompleter = Callable[[str, str], Awaitable[str]]

# Type alias for the ingest-context callable so tests can swap a fake
# without spinning brain. Receives (project_id, owner_id) → context.
ContextFetcher = Callable[[str, str], Awaitable[IngestContext]]

# Type alias for the source-resolution callable so tests can swap a fake
# without spinning brain. Receives (source_id, owner_id) → text body or
# None if the source is gone. Errors raise; the runner converts those
# into a `failed` update.
SourceResolver = Callable[[str, str], "Optional[str]"]


async def handle_message(
    raw: bytes,
    *,
    cfg: Config,
    publish: Publisher,
    llm_stream: Optional[LLMStreamer] = None,
    llm_complete: Optional[LLMCompleter] = None,
    source_resolver: Optional[SourceResolver] = None,
    context_fetcher: Optional[ContextFetcher] = None,
    cancel_registry: Optional[CancelRegistry] = None,
    model_resolver: Optional[DefaultModelResolver] = None,
    preference_resolver: Optional[PreferenceModelResolver] = None,
) -> Optional[IngestRequest]:
    """Decode one inbound NATS message and run the full pipeline.

    Returns the parsed request on success, None on bad payload. Tests
    pass ``llm_stream`` / ``llm_complete`` to inject deterministic
    fakes; production uses the defaults which call hub via
    ``llm.stream_messages`` / ``llm.complete_messages``. ``model_resolver``
    is the process-level default-chat-model cache (``run()`` creates and
    warms one); tests may omit it. ``preference_resolver`` is the
    process-level per-owner ingest-model preference cache (identity);
    omitted = preference layer disabled.
    """
    try:
        payload = json.loads(raw.decode("utf-8"))
    except json.JSONDecodeError as e:
        logger.warning("wiki_llm: bad json: %s", e)
        return None
    # brain 的 BusPublisher 把业务 payload 包进 {topic, kind, payload} 信封
    # （services/brain/internal/publisher/bus.go）；rescan/测试路径直接给
    # 业务层。两种形态都接受。
    if isinstance(payload, dict) and isinstance(payload.get("payload"), dict):
        payload = payload["payload"]
    try:
        req = IngestRequest.from_payload(payload)
    except ValueError as e:
        logger.warning("wiki_llm: invalid request: %s", e)
        return None

    logger.info("wiki_llm: accepted task=%s project=%s source=%s text_chars=%d",
                req.task_id, req.project_id, req.source_id,
                len(req.raw_text))

    # Cancelled while still queued → don't burn an LLM call. The
    # ``cancelled`` update flips the brain row terminal (the API only
    # sets a flag; the worker owns the transition).
    if cancel_registry is not None and cancel_registry.is_cancelled(req.task_id):
        logger.info("wiki_llm: task=%s cancelled before pickup", req.task_id)
        await _emit(publish, cfg, Update(task_id=req.task_id, kind=KIND_CANCELLED))
        return req

    await _emit(publish, cfg, Update(task_id=req.task_id, kind=KIND_RUNNING))

    # Resolve the source content. Inline raw_text wins when present
    # (it's already on the wire and saves a round trip). When absent,
    # the source-id reverse path resolves through brain — see
    # workers/wiki-llm/wiki_llm/brain.py for the contract.
    source_text = (req.raw_text or "").strip()
    source_title = req.title or ""
    if not source_text and req.source_id:
        resolver = source_resolver if source_resolver is not None \
            else _default_source_resolver(cfg)
        try:
            fetched = await resolver(req.source_id, req.owner_id)
        except BrainClientError as e:
            await _emit(publish, cfg, Update(
                task_id=req.task_id, kind=KIND_FAILED,
                error=f"source fetch failed: {e}",
            ))
            return req
        if fetched is None:
            await _emit(publish, cfg, Update(
                task_id=req.task_id, kind=KIND_FAILED,
                error=f"source {req.source_id} not found on brain",
            ))
            return req
        # Resolver returns the raw body OR a (body, title) tuple — the
        # default resolver returns just the body string. Tests can
        # swap in a richer resolver if they need to test title fallback.
        if isinstance(fetched, tuple):
            source_text = (fetched[0] or "").strip()
            if not source_title and len(fetched) > 1:
                source_title = fetched[1] or ""
        else:
            source_text = fetched.strip()

    if not source_text:
        await _emit(publish, cfg, Update(
            task_id=req.task_id, kind=KIND_FAILED,
            error=("task has no content: "
                   "raw_text empty and source_id either missing "
                   "or resolves to an empty source body"),
        ))
        return req

    # Patch the request title in case we picked one up from the source
    # row above; downstream prompt builder uses req.title directly.
    if source_title and not req.title:
        # Replace the request with one that carries the resolved title;
        # the original is frozen so we shadow it locally.
        req = IngestRequest(
            task_id=req.task_id, project_id=req.project_id,
            owner_id=req.owner_id, title=source_title,
            raw_text=req.raw_text, source_id=req.source_id,
        )

    # 模型兜底链(B2,对齐 brain ChatRunner.defaultChatModel 并插入
    # 任务 owner 的个人偏好一级):
    # BIUMIND_WIKI_LLM_MODEL env 显式覆盖 > owner 偏好(identity 内部
    # 端点) > relay default-chat 端点(admin 在 models 表标
    # is_default_chat) > 内置硬编码兜底。任一端点不可达 / 未配不报错,
    # 落下一级(与 brain 一致)。
    model, model_source = await _resolve_model(
        cfg, model_resolver, req.owner_id, preference_resolver,
    )

    error = await _run_attempt(
        req=req, source_text=source_text, cfg=cfg, publish=publish,
        llm_stream=llm_stream, llm_complete=llm_complete,
        context_fetcher=context_fetcher, cancel_registry=cancel_registry,
        model=model, idem_suffix="",
    )

    # 弱模型质量兜底：模型来自 owner 偏好且任务失败时,自动用「去掉
    # 偏好层的链」重解析出模型重跑一次;仍失败才发 failed。非偏好来源
    # (env/default/builtin)失败不重跑 —— 那些是运维 / 平台显式选择,
    # 重跑只是浪费配额。重跑时 stage-2 的 idempotency_key 换成
    # task_id + ":fallback",防 relay Hold 去重吃掉重试。
    if error is not None and model_source == "preference":
        fallback_model, fallback_source = await _resolve_model(
            cfg, model_resolver, req.owner_id, preference_resolver,
            skip_preference=True,
        )
        logger.warning(
            "wiki_llm: task=%s preference model %s failed (%s); "
            "retrying once with %s (source=%s, preference layer skipped)",
            req.task_id, model, error, fallback_model, fallback_source,
        )
        error = await _run_attempt(
            req=req, source_text=source_text, cfg=cfg, publish=publish,
            llm_stream=llm_stream, llm_complete=llm_complete,
            context_fetcher=context_fetcher,
            cancel_registry=cancel_registry,
            model=fallback_model, idem_suffix=":fallback",
        )
        if error is not None:
            # 终态 failed 的文本如实反映已降级重试过一次。
            error = (f"{error} (retried once with model "
                     f"{fallback_model} after preference-model failure)")
        else:
            logger.info(
                "wiki_llm: task=%s fallback retry with %s succeeded",
                req.task_id, fallback_model,
            )

    if error is not None:
        await _emit(publish, cfg, Update(
            task_id=req.task_id, kind=KIND_FAILED, error=error,
        ))

    return req


async def _run_attempt(
    *,
    req: IngestRequest,
    source_text: str,
    cfg: Config,
    publish: Publisher,
    llm_stream: Optional[LLMStreamer],
    llm_complete: Optional[LLMCompleter],
    context_fetcher: Optional[ContextFetcher],
    cancel_registry: Optional[CancelRegistry],
    model: str,
    idem_suffix: str,
) -> Optional[str]:
    """用模型 ``model`` 跑一遍完整的 stage1+stage2,可重入(fallback 重跑)。

    返回 None = 成功(done)或取消(cancelled)—— 这两种终态 update 已在
    内部发出;返回错误字符串 = 失败但 **尚未** 发 failed update,由
    handle_message 决定是降级重跑还是发终态 failed。

    ``idem_suffix``:重跑时传 ":fallback",拼进 stage-2 / stage-1 的
    idempotency_key(分别成 ``task:fallback`` / ``task:fallback:analyze``),
    防 relay Hold 去重把重试当成首次调用的重复投递。
    """
    idem_base = req.task_id + idem_suffix

    # P2 #17 两阶段 CoT（默认开，BIUMIND_WIKI_LLM_TWO_STAGE=0 关）：
    #   stage 1 — 非流式分析调用（实体/概念/与现有 wiki 关联/矛盾/建议
    #             页面划分），输入带项目上下文（purpose/schema/页面索引）
    #   stage 2 — 现有流式 FILE 块生成，prompt 吃 stage 1 分析 + 同一上下文
    # Stage 1 失败 = 任务 failed（质量门，不降级裸跑 stage 2）；上下文
    # 拉取失败只 warn 降级为空上下文（滚动发布容忍：旧 brain 无端点）。
    analysis: Optional[str] = None
    context: Optional[IngestContext] = None
    if cfg.two_stage:
        fetcher = context_fetcher if context_fetcher is not None \
            else _default_context_fetcher(cfg)
        try:
            context = await fetcher(req.project_id, req.owner_id)
        except BrainClientError as e:
            logger.warning("wiki_llm: ingest-context fetch failed task=%s: %s "
                           "(degrading to empty context)", req.task_id, e)
            context = None
        except Exception as e:  # noqa: BLE001 — test stubs may raise anything
            logger.warning("wiki_llm: ingest-context fetch error task=%s: %s "
                           "(degrading to empty context)", req.task_id, e)
            context = None

        completer = (
            llm_complete if llm_complete is not None
            else _default_completer(cfg, owner_id=req.owner_id,
                                    task_id=idem_base, model=model)
        )
        from .prompts import ANALYZE_SYSTEM_PROMPT, build_analyze_prompt
        try:
            analysis = await completer(
                ANALYZE_SYSTEM_PROMPT,
                build_analyze_prompt(
                    source_title=req.title or "",
                    source_text=source_text,
                    context=context,
                ),
            )
        except LLMError as e:
            # 质量门：stage 1 失败 = 任务失败，不降级裸跑 stage 2
            # （否则两阶段形同虚设，且静默质量回退无法观测）。
            logger.warning("wiki_llm: stage-1 llm error task=%s: %s",
                           req.task_id, e)
            return f"stage-1 analysis failed: {e}"
        if not (analysis or "").strip():
            # 质量门：空分析等于 stage 1 失败——裸跑 stage 2 就退回
            # 单阶段质量，违背两阶段的存在意义。
            return "stage-1 analysis returned empty (quality gate)"
        logger.info("wiki_llm: stage-1 analysis done task=%s chars=%d "
                    "context_pages=%d", req.task_id, len(analysis),
                    len(context.pages) if context else 0)

    streamer = (
        llm_stream if llm_stream is not None
        else _default_streamer(cfg, owner_id=req.owner_id,
                               task_id=idem_base, model=model)
    )

    try:
        return await _run_pipeline(
            req=req,
            source_text=source_text,
            streamer=streamer,
            cfg=cfg,
            publish=publish,
            cancel_registry=cancel_registry,
            analysis=analysis,
            context=context,
        )
    except LLMError as e:
        logger.warning("wiki_llm: llm error task=%s: %s", req.task_id, e)
        return str(e)
    except Exception as e:  # noqa: BLE001 — last-resort, surface as failed
        logger.exception("wiki_llm: unexpected error task=%s", req.task_id)
        return f"unexpected: {e}"


async def _run_pipeline(
    *,
    req: IngestRequest,
    source_text: str,
    streamer: LLMStreamer,
    cfg: Config,
    publish: Publisher,
    cancel_registry: Optional[CancelRegistry] = None,
    analysis: Optional[str] = None,
    context: Optional[IngestContext] = None,
) -> Optional[str]:
    """Stream the LLM, emit per-page updates, finish with done.

    返回 None = done / cancelled（终态 update 已发出）；返回错误字符串
    = 失败但 **尚未** 发 failed update（由调用方决定降级重跑或发终态）。
    """
    from .prompts import SYSTEM_PROMPT, build_user_prompt

    user = build_user_prompt(
        source_title=req.title or "",
        source_text=source_text,
        analysis=analysis,
        context=context,
    )

    buffer = ""
    emitted_paths: set[str] = set()
    page_index = 0
    delta_count = 0
    stream_start = asyncio.get_event_loop().time()
    first_delta_at: Optional[float] = None

    async for delta in streamer(SYSTEM_PROMPT, user):
        # Chunk boundary cancel check: abort the stream promptly instead
        # of running a paid LLM call to completion.
        if cancel_registry is not None and cancel_registry.is_cancelled(req.task_id):
            logger.info("wiki_llm: task=%s cancelled mid-stream at delta=%d pages=%d",
                        req.task_id, delta_count, page_index)
            await _emit(publish, cfg, Update(
                task_id=req.task_id, kind=KIND_CANCELLED,
                progress={"pages_total": page_index},
            ))
            return
        if not delta:
            continue
        delta_count += 1
        if first_delta_at is None:
            first_delta_at = asyncio.get_event_loop().time()
            logger.debug(
                "wiki_llm: first delta task=%s first_chunk_ms=%d",
                req.task_id,
                int((first_delta_at - stream_start) * 1000),
            )
        buffer += delta
        # Re-parse on every delta; the parser is cheap and idempotent.
        # We only care about closed blocks (warnings here mean "still
        # streaming" or "unsafe path attempted") — warnings get logged
        # but don't fail the task; the unsafe-path block was already
        # dropped by the parser.
        result = parse_file_blocks(buffer)
        for block in result.blocks:
            if block.path in emitted_paths:
                continue
            page_index += 1
            await _emit_page(publish, cfg, req.task_id, block, page_index)
            emitted_paths.add(block.path)

    # Stream ended. One final parse to catch a block that closed on the
    # very last delta (the per-delta parse loop above would have caught
    # it, but defensive re-check costs nothing).
    final = parse_file_blocks(buffer)
    for block in final.blocks:
        if block.path in emitted_paths:
            continue
        page_index += 1
        await _emit_page(publish, cfg, req.task_id, block, page_index)
        emitted_paths.add(block.path)

    if final.warnings:
        logger.info("wiki_llm: task=%s parse warnings: %s",
                    req.task_id, "; ".join(final.warnings))

    # Stream truncation = task failure. Pages already emitted stay saved
    # (partial-save), but the task must not report success with pages
    # silently dropped — the user retries to get the full set.
    truncated = [w for w in final.warnings if "not closed (stream truncated)" in w]
    if truncated:
        return ("LLM stream truncated: " + "; ".join(truncated) +
                f" ({page_index} page(s) saved before truncation)")

    if not emitted_paths:
        # The model output zero parseable FILE blocks — typically means
        # the prompt didn't take and the response was prose. Fail the
        # task so the user sees something actionable instead of a
        # silent green checkmark.
        # Log first 500 chars of buffer so operators can diagnose.
        logger.warning("wiki_llm: empty result task=%s buffer_head=%r",
                       req.task_id, buffer[:500])
        return ("LLM produced no parseable FILE blocks "
                "(see worker logs for response sample)")

    logger.debug(
        "wiki_llm: stream done task=%s deltas=%d pages=%d total_ms=%d buffer_chars=%d",
        req.task_id, delta_count, page_index,
        int((asyncio.get_event_loop().time() - stream_start) * 1000),
        len(buffer),
    )
    await _emit(publish, cfg, Update(
        task_id=req.task_id, kind=KIND_DONE,
        progress={"pages_total": page_index},
    ))
    return None


async def _emit_page(
    publish: Publisher,
    cfg: Config,
    task_id: str,
    block: FileBlock,
    index: int,
) -> None:
    """Send one page update for a closed FILE block.

    Content passes through the write-time sanitizer first — real-corpus
    audits show ~45% of LLM pages carry malformed frontmatter (fence
    wrappers / stray keys / missing openers); brain stores body_md
    verbatim, so this is the only point the corruption can be stopped.
    """
    title = _extract_title(block)
    update = Update(
        task_id=task_id,
        kind=KIND_PAGE,
        path=block.path,
        title=title,
        content=sanitize_ingested_content(block.content),
        index=index,
    )
    await _emit(publish, cfg, update)


def _extract_title(block: FileBlock) -> str:
    """Pull a human title from the block's frontmatter or first heading.

    Order of preference:
      1. ``title`` field of the YAML frontmatter
      2. First-line H1 (``# Title``) of the body, after frontmatter strip
      3. Empty (brain's subscriber falls back to path basename)
    """
    fm = parse_frontmatter(block.content)
    if fm.frontmatter is not None:
        title = fm.frontmatter.get("title")
        if isinstance(title, str) and title.strip():
            return title.strip()
    body = fm.body
    for line in body.split("\n", 5):  # only inspect head for cheapness
        stripped = line.strip()
        if stripped.startswith("# "):
            return stripped[2:].strip()
    return ""


async def _emit(publish: Publisher, cfg: Config, update: Update) -> None:
    payload = update.to_payload()
    # brain 侧 subscriber（ingest/subscriber.go envWire）按 BusPublisher 约定
    # 解 {topic, kind, payload} 信封 —— 本 worker 作为 bus 发布者对齐同一
    # 线格式（信封 kind 与 payload.kind 同值）。
    body = json.dumps({
        "topic": "wiki.ingest.update",
        "kind": str(payload.get("kind", "")),
        "payload": payload,
    }, ensure_ascii=False).encode("utf-8")
    await publish(cfg.update_subject, body)


async def _resolve_model(
    cfg: Config,
    model_resolver: Optional[DefaultModelResolver],
    owner_id: str = "",
    preference_resolver: Optional[PreferenceModelResolver] = None,
    skip_preference: bool = False,
) -> Tuple[str, str]:
    """解析本任务用的模型 code,返回 ``(model, source)``。

    兜底链(对齐 brain ChatRunner.defaultChatModel,并插入任务 owner
    的个人偏好一级):

      1. ``BIUMIND_WIKI_LLM_MODEL`` env 显式覆盖 —— 设了就用它,
         不查任何端点(运维强制切换通道);source="env";
      2. owner 的 ingest 模型偏好 —— identity
         ``GET /v1/internal/settings/{owner_id}/ingest-model``,
         per-owner 进程内缓存(60s 命中 / 10s 负缓存,见
         preference_model.py);source="preference";
      3. relay ``GET /v1/internal/models/default-chat`` —— admin 在
         models 表标 is_default_chat 的平台默认,进程内缓存
         (60s 命中 / 10s 负缓存,见 default_model.py);source="default";
      4. ``BUILTIN_FALLBACK_MODEL`` 硬编码兜底 —— 端点不可达 / 未配 /
         resolver 禁用时落这里,不报错(与 brain 一致:relay 挂了 chat
         不该全废);source="builtin"。

    ``skip_preference=True`` 跳过第 2 级 —— 弱模型质量兜底重跑时
    用(handle_message:preference 来源的模型跑失败后,用去掉偏好层
    的链重解析重试一次)。

    ``model_resolver`` / ``preference_resolver`` 为空时现场构造
    (单测 / 非 run() 入口);生产路径 ``run()`` 总是传进程级实例以
    共享缓存。``identity_url`` 为空时偏好层禁用,解析退化为 B3 三级
    (向后兼容)。
    """
    if cfg.model:
        return cfg.model, "env"
    if owner_id and not skip_preference:
        pref = preference_resolver if preference_resolver is not None \
            else PreferenceModelResolver(cfg.identity_url,
                                         cfg.relay_internal_token)
        m = await pref.preference_model(owner_id)
        if m:
            return m, "preference"
    resolver = model_resolver if model_resolver is not None \
        else DefaultModelResolver(cfg.hub_url, cfg.relay_internal_token)
    m = await resolver.default_chat_model()
    if m:
        return m, "default"
    logger.warning("wiki_llm: relay default-chat unavailable; "
                   "falling back to built-in default %s",
                   BUILTIN_FALLBACK_MODEL)
    return BUILTIN_FALLBACK_MODEL, "builtin"


def _default_streamer(
    cfg: Config, *, owner_id: str, task_id: str, model: str,
) -> LLMStreamer:
    """Return the production streamer that calls model-relay via ``llm``.

    Lazily constructed so unit tests that pass ``llm_stream=`` never
    touch the network configuration. Per-task identity is baked in here:
    ``user_id`` (= owner) drives billing/BYOK attribution on the relay
    internal lane, ``idempotency_key`` (= task_id; preference-fallback
    reruns append ``:fallback`` so the relay Hold dedup doesn't swallow
    the retry) dedups NATS redeliveries. ``model`` comes from
    ``_resolve_model``.
    """
    llm_cfg = LLMConfig(
        base_url=cfg.hub_url,
        token=cfg.relay_internal_token,
        model=model,
        user_id=owner_id,
        idempotency_key=task_id,
    )

    def _call(system: str, user: str):
        return stream_messages(llm_cfg, system=system, user=user)

    return _call


def _default_completer(
    cfg: Config, *, owner_id: str, task_id: str, model: str,
) -> LLMCompleter:
    """Return the production one-shot caller for the stage-1 analysis.

    Same internal lane as the stage-2 streamer (user_id=owner for
    billing/BYOK attribution). The idempotency key gets an ``:analyze``
    suffix so the two LLM calls of one task never collide on the
    relay's Hold dedup — stage 2 keeps the bare task_id (unchanged from
    the single-stage era, so reaper redeliveries of pre-P2 tasks still
    dedup against their original Hold). Output budget is cut to 4K
    tokens: the analysis prompt demands ≤ 600 words, and a runaway
    stage-1 response only burns money without improving stage 2.
    """
    llm_cfg = LLMConfig(
        base_url=cfg.hub_url,
        token=cfg.relay_internal_token,
        model=model,
        user_id=owner_id,
        idempotency_key=f"{task_id}:analyze",
        max_tokens=4096,
    )

    async def _call(system: str, user: str) -> str:
        return await complete_messages(llm_cfg, system=system, user=user)

    return _call


def _default_context_fetcher(cfg: Config) -> ContextFetcher:
    """Return the production ingest-context fetcher that calls brain.

    When brain isn't configured (no URL / token) the fetcher returns an
    empty context instead of raising — inline-raw_text tasks are
    perfectly usable without project context, and the stage-1 prompt
    simply omits the wiki sections.
    """
    brain_cfg = BrainConfig(
        base_url=cfg.brain_url,
        internal_token=cfg.internal_token,
    )

    async def _call(project_id: str, owner_id: str) -> IngestContext:
        if not cfg.brain_url or not cfg.internal_token:
            return IngestContext()
        return await fetch_ingest_context(
            brain_cfg, project_id=project_id, owner_id=owner_id,
        )

    return _call


def _default_source_resolver(cfg: Config) -> SourceResolver:
    """Return the production source resolver that calls brain.

    Returns a tuple ``(raw, title)`` so callers can pick up a title from
    the source row when the original request didn't carry one. The
    runner unpacks both.
    """
    brain_cfg = BrainConfig(
        base_url=cfg.brain_url,
        internal_token=cfg.internal_token,
    )

    async def _call(source_id: str, owner_id: str):
        row = await fetch_source(brain_cfg, source_id=source_id, owner_id=owner_id)
        if row is None:
            return None
        return (row.raw, row.title)

    return _call


async def run(cfg: Config) -> None:
    """Top-level entry point. Connects NATS, subscribes, blocks until
    cancelled. Imported lazily so unit tests don't need ``nats-py``."""
    import nats  # type: ignore

    nc = await nats.connect(cfg.nats_url, name="biumind-wiki-llm")
    logger.info("wiki_llm worker connected nats=%s sub=%s queue=%s",
                cfg.nats_url, cfg.request_subject, cfg.queue_group)

    cancels = CancelRegistry()

    # 进程级默认模型 resolver(env 未覆盖时生效),启动即异步预热缓存
    # (对齐 brain main.go 的 go resolver.Warm),首个任务不付 relay 往返。
    model_resolver = DefaultModelResolver(cfg.hub_url, cfg.relay_internal_token)
    warm_task = asyncio.create_task(model_resolver.warm())

    # 进程级 per-owner 偏好 resolver(identity 内部端点);identity_url
    # 为空时整层禁用(向后兼容)。per-owner 缓存无法按 owner 预热,首个
    # 任务付一次 identity 往返,负缓存兜底 identity 短时不可用。
    preference_resolver = PreferenceModelResolver(
        cfg.identity_url, cfg.relay_internal_token,
    )

    async def _publish(subject: str, body: bytes) -> None:
        await nc.publish(subject, body)

    async def _handle(msg) -> None:  # noqa: ANN001 — nats msg dynamic
        try:
            await handle_message(msg.data, cfg=cfg, publish=_publish,
                                 cancel_registry=cancels,
                                 model_resolver=model_resolver,
                                 preference_resolver=preference_resolver)
        except Exception:  # noqa: BLE001 — one bad job mustn't kill loop
            logger.exception("wiki_llm: handler crashed")

    async def _handle_cancel(msg) -> None:  # noqa: ANN001 — nats msg dynamic
        tid = parse_cancel_task_id(msg.data)
        if tid is None:
            logger.warning("wiki_llm: bad cancel broadcast: %r", msg.data[:200])
            return
        logger.info("wiki_llm: cancel signal task=%s", tid)
        cancels.add(tid)

    await nc.subscribe(cfg.request_subject, queue=cfg.queue_group, cb=_handle)
    # Broadcast subscription — intentionally NO queue group: every worker
    # instance must observe every cancel, not just the one holding the task.
    await nc.subscribe(cfg.cancel_subject, cb=_handle_cancel)
    try:
        stop = asyncio.Event()
        await stop.wait()
    finally:
        warm_task.cancel()
        await nc.drain()
