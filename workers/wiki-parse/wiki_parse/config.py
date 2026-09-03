"""Phase 3 wiki-parse worker config. All from env, read once at startup.

Env:
  BIUMIND_NATS_URL / NATS_URL     NATS 连接（默认 nats://localhost:4222）
  BIUMIND_ENV                     环境前缀（默认 dev，拼进 subject）
  BIUMIND_BRAIN_URL               brain 内部端点 base URL（必填）
  BIUMIND_INTERNAL_TOKEN          与 brain 共享的 service token（必填）
  BIUMIND_WIKI_PARSE_QUEUE        NATS queue group（默认 brain-wiki-parse）
  BIUMIND_WIKI_PARSE_INTERVAL_S   rescan tick 间隔秒（默认 60）
  BIUMIND_WIKI_PARSE_MAX_BYTES    单文件大小上限（默认 200MB，zip-bomb 防护）
  BIUMIND_WIKI_PARSE_OCR_ENABLED  PDF OCR 开关（默认 false；启用后全量 PDF 走
                                  自部署 MinerU，失败降级 pypdf，B1 D1/D5）
  BIUMIND_MINERU_API_BASE         mineru-api base URL（默认 http://mineru:8000，
                                  内网服务间调用，不经 nginx 不暴露公网）
  BIUMIND_OCR_POLL_TIMEOUT_S      MinerU 轮询超时秒（默认 900）
  BIUMIND_WIKI_PARSE_MAX_CONCURRENCY  并发 job 上限（默认 4；OCR 单任务分钟级，
                                  顺序 await 会堵整队）
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
    ocr_enabled: bool
    mineru_api_base: str
    ocr_poll_timeout_s: int
    max_concurrency: int

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
            ocr_enabled=(
                e.get("BIUMIND_WIKI_PARSE_OCR_ENABLED", "false").lower()
                in {"1", "true", "yes", "on"}
            ),
            mineru_api_base=e.get(
                "BIUMIND_MINERU_API_BASE", "http://mineru:8000"
            ),
            ocr_poll_timeout_s=int(e.get("BIUMIND_OCR_POLL_TIMEOUT_S", "900")),
            max_concurrency=int(e.get("BIUMIND_WIKI_PARSE_MAX_CONCURRENCY", "4")),
        )
