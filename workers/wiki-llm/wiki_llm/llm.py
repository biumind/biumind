"""HTTP client for model-relay's internal ``POST /v1/internal/chat``.

Internal-lane variant of ``/v1/messages`` (see
services/model-relay/internal/internalapi/chat.go): the worker
authenticates with the shared internal bearer token, and the body
carries ``user_id`` (= task owner) so Hold/Settle billing and BYOK
resolution attribute to that user — identical to the user-facing path.
``idempotency_key`` (= task_id) makes NATS redeliveries safe against
double-charging.

Responses are Server-Sent Events in the unified frame format (same as
the public endpoint's default; see
services/model-relay/internal/api/messages.go):

  event: delta            data: {"text": "..."}
  event: tool_call_start  data: {"id": "...", "name": "..."}
  event: tool_call_args   data: {"id": "...", "delta": "..."}
  event: tool_call_end    data: {"id": "..."}
  event: stop             data: {"reason": "..."}
  event: end              data: {}
  event: error            data: {"error": {...}}

For wiki ingest we only consume ``delta`` (the text we accumulate into
the FILE-block buffer), ``end`` / ``stop`` (terminal), and ``error``
(propagate as exception so the runner emits a `failed` update).

The streaming generator yields each delta as it arrives so the runner
can re-parse the cumulative buffer after each chunk and emit
incremental ``page`` updates — this is how streaming partial-save works
on biumind: the parser is idempotent and the diff is "blocks since the
last call".
"""

from __future__ import annotations

import json
import logging
from dataclasses import dataclass
from typing import AsyncIterator, Optional

import httpx


logger = logging.getLogger("biumind.wiki_llm.llm")


class LLMError(RuntimeError):
    """Raised on any non-recoverable model-relay failure."""


@dataclass(frozen=True)
class LLMConfig:
    base_url: str
    token: str
    model: str
    # 计费归属：任务 owner（= ingest payload 的 owner_id）。内部车道
    # 必填，model-relay 据此注入 claims 做 Hold/Settle + BYOK 查询。
    user_id: str = ""
    # 幂等键：task_id。NATS 重投同一任务时 model-relay 的 Hold 去重。
    idempotency_key: str = ""
    # Cap output budget. Wiki pages are typically 200-1500 tokens each,
    # 5-15 pages per source → ~10K tokens of output. 16K is a safe
    # default that fits inside most provider context windows.
    max_tokens: int = 16384
    # Read timeout. Streaming connections can sit idle while the model
    # thinks; we want to allow long pauses without aborting. The client
    # connect/write timeouts stay short to fail fast on misconfig.
    read_timeout_s: float = 600.0
    connect_timeout_s: float = 10.0


async def stream_messages(
    cfg: LLMConfig,
    *,
    system: str,
    user: str,
) -> AsyncIterator[str]:
    """Open a streaming chat with model-relay and yield text deltas.

    Yields each ``delta.text`` as it arrives. Raises LLMError on any
    transport / status / SSE-error condition. The caller is responsible
    for accumulating yielded chunks; this generator does no buffering
    of its own beyond what httpx and the underlying TCP layer provide.

    Cancellation: cancelling the generator (via ``aclose()`` or task
    cancellation) closes the upstream connection promptly, which Hub
    forwards to the provider — a properly-implemented Anthropic
    connection will stop generation on disconnect.
    """
    if not cfg.base_url:
        raise LLMError("relay base_url missing — set BIUMIND_HUB_URL")
    if not cfg.user_id:
        # 内部车道硬要求：缺 user_id 端点直接 400，提前给出可读错误。
        raise LLMError("relay user_id missing — task payload has no owner_id")

    payload = {
        "model": cfg.model,
        "stream": True,
        "max_tokens": cfg.max_tokens,
        "system": system,
        "messages": [
            {"role": "user", "content": user},
        ],
        "user_id": cfg.user_id,
    }
    if cfg.idempotency_key:
        payload["idempotency_key"] = cfg.idempotency_key
    url = cfg.base_url.rstrip("/") + "/v1/internal/chat"
    headers = {
        "Content-Type": "application/json",
        "Accept": "text/event-stream",
    }
    if cfg.token:
        headers["Authorization"] = f"Bearer {cfg.token}"

    timeout = httpx.Timeout(
        connect=cfg.connect_timeout_s,
        read=cfg.read_timeout_s,
        write=cfg.connect_timeout_s,
        pool=cfg.connect_timeout_s,
    )

    async with httpx.AsyncClient(timeout=timeout) as client:
        try:
            async with client.stream("POST", url, json=payload, headers=headers) as resp:
                if resp.status_code >= 400:
                    body = await resp.aread()
                    text = body.decode("utf-8", errors="replace")
                    raise LLMError(
                        f"relay returned HTTP {resp.status_code}: {text[:500]}"
                    )

                async for chunk in _iter_sse_deltas(resp):
                    yield chunk
        except httpx.HTTPError as e:
            raise LLMError(f"relay request failed: {e}") from e


async def _iter_sse_deltas(resp: httpx.Response) -> AsyncIterator[str]:
    """Parse the SSE stream and yield text deltas only.

    SSE framing per W3C: each event is a sequence of `field: value`
    lines terminated by a blank line. We only consume `event:` and
    `data:` fields; comments and other fields pass through.

    Hub guarantees one JSON object per `data:` field, so we don't have
    to handle multi-line `data:` concatenation.
    """
    cur_event: Optional[str] = None
    async for line in resp.aiter_lines():
        if line == "":
            # Blank line = event boundary; nothing to do because we
            # process each ``event``/``data`` pair as it arrives below.
            cur_event = None
            continue
        if line.startswith(":"):
            # Comment / heartbeat.
            continue
        if line.startswith("event:"):
            cur_event = line[len("event:"):].strip()
            continue
        if line.startswith("data:"):
            data = line[len("data:"):].strip()
            if not data:
                continue
            if cur_event == "delta":
                try:
                    payload = json.loads(data)
                except json.JSONDecodeError:
                    logger.warning("wiki_llm.llm: bad delta json: %r", data[:200])
                    continue
                text = payload.get("text", "")
                if isinstance(text, str) and text:
                    yield text
            elif cur_event == "error":
                # Propagate to caller as exception. We still drain the
                # connection by exiting the iterator cleanly.
                raise LLMError(f"relay error frame: {data[:500]}")
            elif cur_event in ("end", "stop"):
                # Treat as end-of-stream; httpx will close the connection
                # naturally when we exit the async-for.
                return
            # tool_call_* frames ignored for the wiki ingest path.
