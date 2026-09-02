"""Worker configuration loaded from env vars.

Subject naming follows the same biumind.<env>.brain.* convention used by
the Go publisher (services/brain/internal/publisher.NewBus) so a NATS
client tail tail-subscribes work uniformly across services::

    BIUMIND_NATS_URL          NATS url; default nats://localhost:4222
    BIUMIND_ENV               environment slug; default "dev"
    BIUMIND_WIKI_LLM_QUEUE    queue group; default "brain-wiki-llm"
    BIUMIND_WIKI_LLM_TIMEOUT_S per-task budget; default 600 (10 min)

LLM endpoint — the worker calls model-relay's internal lane
``POST /v1/internal/chat`` (I6: never the model provider directly),
authenticating with the shared internal token and attributing cost to
the task owner via the body ``user_id`` field (same pattern as the aigc
hotparse worker)::

    BIUMIND_HUB_URL               e.g. http://model-relay:7001
    BIUMIND_RELAY_INTERNAL_TOKEN  shared internal bearer (same value as
                                  model-relay's IDENTITY_INTERNAL_TOKEN);
                                  supersedes the old per-user
                                  BIUMIND_HUB_TOKEN
    BIUMIND_WIKI_LLM_MODEL    default "claude-haiku-4-5-20251001"

Pipeline shape (P2 #17)::

    BIUMIND_WIKI_LLM_TWO_STAGE  "1" (default) = two-stage CoT: stage 1
                                structured analysis (non-streaming) fed
                                into stage 2 FILE-block generation
                                (streaming partial-save). "0"/"false"/
                                "off" = legacy single-stage, kept as a
                                kill-switch for prod A/B comparison.
"""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Config:
    nats_url: str
    env: str
    queue_group: str
    timeout_s: int

    hub_url: str
    # model-relay 内部车道共享密钥（= model-relay IDENTITY_INTERNAL_TOKEN）。
    # 计费归属不走 token —— 每个任务按 payload owner_id 在 body 里显式带
    # user_id（见 llm.py / runner._default_streamer）。
    relay_internal_token: str
    model: str

    # Brain reverse callback. Used by the source-id-only ingest path
    # (P2-B) — when a task arrives with no inline raw_text, the worker
    # GETs /v1/internal/wiki/sources/{id} on brain to fetch the source
    # body. Empty brain_url disables the path; the worker falls back to
    # rejecting source-id-only tasks with a clear failure reason.
    brain_url: str
    internal_token: str

    # P2 #17 两阶段管线开关。默认开：stage 1 分析（非流式）+ stage 2
    # FILE 块生成（流式 partial-save）。BIUMIND_WIKI_LLM_TWO_STAGE=0
    # 回退到旧单阶段（直接产 FILE 块），便于线上 A/B 对比输出质量。
    two_stage: bool = True

    @property
    def request_subject(self) -> str:
        # Subscribed to (brain → worker).
        return f"biumind.{self.env}.brain.wiki.ingest.requested"

    @property
    def update_subject(self) -> str:
        # Published to (worker → brain): status / page-added / terminal.
        return f"biumind.{self.env}.brain.wiki.ingest.update"

    @property
    def cancel_subject(self) -> str:
        # Subscribed to (brain → worker, broadcast): task cancel signals.
        # No queue group — every worker instance must hear every cancel.
        return f"biumind.{self.env}.brain.wiki.ingest.cancel"

    @classmethod
    def from_env(cls, env: dict | None = None) -> "Config":
        e = env if env is not None else os.environ
        return cls(
            nats_url=(e.get("BIUMIND_NATS_URL")
                      or e.get("NATS_URL")
                      or "nats://localhost:4222"),
            env=e.get("BIUMIND_ENV", "dev"),
            queue_group=e.get("BIUMIND_WIKI_LLM_QUEUE", "brain-wiki-llm"),
            timeout_s=int(e.get("BIUMIND_WIKI_LLM_TIMEOUT_S", "600")),
            hub_url=e.get("BIUMIND_HUB_URL", ""),
            relay_internal_token=e.get("BIUMIND_RELAY_INTERNAL_TOKEN", ""),
            model=e.get("BIUMIND_WIKI_LLM_MODEL", "claude-haiku-4-5-20251001"),
            brain_url=e.get("BIUMIND_BRAIN_URL", ""),
            internal_token=e.get("BIUMIND_INTERNAL_TOKEN", ""),
            two_stage=e.get("BIUMIND_WIKI_LLM_TWO_STAGE", "1").strip().lower()
                      not in ("0", "false", "no", "off"),
        )
