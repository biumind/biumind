"""Phase 3 wiki-parse worker config. All from env, read once at startup.

Env:
  BIUMIND_NATS_URL / NATS_URL     NATS 连接（默认 nats://localhost:4222）
  BIUMIND_ENV                     环境前缀（默认 dev，拼进 subject）
  BIUMIND_BRAIN_URL               brain 内部端点 base URL（必填）
  BIUMIND_INTERNAL_TOKEN          与 brain 共享的 service token（必填）
  BIUMIND_WIKI_PARSE_QUEUE        NATS queue group（默认 brain-wiki-parse）
  BIUMIND_WIKI_PARSE_INTERVAL_S   rescan tick 间隔秒（默认 60）
  BIUMIND_WIKI_PARSE_MAX_BYTES    单文件大小上限（默认 200MB，zip-bomb 防护）
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Mapping


@dataclass(frozen=True)
class Config:
    nats_url: str
    env: str
    queue_group: str
    brain_url: str
    internal_token: str
    parse_queue_interval_s: int
    max_file_bytes: int

    @property
    def request_subject(self) -> str:
        # 对齐 brain publisher 用法：topic="wiki.parse" + kind="requested"
        # → biumind.<env>.brain.wiki.parse.requested（两段，不重复）。
        # （wiki.ingest 发布端曾有的 topic=kind 重复段 bug 已修，统一两段式。）
        return f"biumind.{self.env}.brain.wiki.parse.requested"

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> "Config":
        e = env if env is not None else os.environ
        return cls(
            nats_url=(
                e.get("BIUMIND_NATS_URL")
                or e.get("NATS_URL")
                or "nats://localhost:4222"
            ),
            env=e.get("BIUMIND_ENV", "dev"),
            queue_group=e.get("BIUMIND_WIKI_PARSE_QUEUE", "brain-wiki-parse"),
            brain_url=e.get("BIUMIND_BRAIN_URL", ""),
            internal_token=e.get("BIUMIND_INTERNAL_TOKEN", ""),
            parse_queue_interval_s=int(
                e.get("BIUMIND_WIKI_PARSE_INTERVAL_S", "60")
            ),
            max_file_bytes=int(
                e.get("BIUMIND_WIKI_PARSE_MAX_BYTES", str(200 * 1024 * 1024))
            ),
        )
