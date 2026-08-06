"""Worker configuration loaded from env vars.

    BIUMIND_NATS_URL           — NATS server URL (default nats://localhost:4222)
    BIUMIND_ENV                — environment slug; default "dev"
    BIUMIND_INGEST_QUEUE       — queue group name; default "brain-ingest-py"
    BIUMIND_INGEST_TIMEOUT_S   — per-job extraction timeout; default 180
    BIUMIND_WHISPER_MODEL      — faster-whisper model id; default "base"
    BIUMIND_WHISPER_DEVICE     — "cpu" | "cuda"; default "cpu"
    BIUMIND_TESSERACT_LANG     — Tesseract language pack; default "eng+chi_sim"
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
    whisper_model: str
    whisper_device: str
    tesseract_lang: str

    @property
    def binary_subject(self) -> str:
        # Subscribed to.
        return f"biumind.{self.env}.brain.ingest.binary"

    @property
    def text_subject(self) -> str:
        # Re-published to after extraction; the Go ingestbus consumer
        # picks up here for two-step CoT + Wiki write.
        return f"biumind.{self.env}.brain.ingest.requested"

    @classmethod
    def from_env(cls, env: dict | None = None) -> "Config":
        e = env if env is not None else os.environ
        return cls(
            # 优先 BIUMIND_NATS_URL (历史命名), fallback NATS_URL (Go 服务 + dev compose 用的名字).
            # 默认 localhost 只在裸机/dev 起服时用, docker 网络里必走显式注入.
            nats_url=e.get("BIUMIND_NATS_URL") or e.get("NATS_URL") or "nats://localhost:4222",
            env=e.get("BIUMIND_ENV", "dev"),
            queue_group=e.get("BIUMIND_INGEST_QUEUE", "brain-ingest-py"),
            timeout_s=int(e.get("BIUMIND_INGEST_TIMEOUT_S", "180")),
            whisper_model=e.get("BIUMIND_WHISPER_MODEL", "base"),
            whisper_device=e.get("BIUMIND_WHISPER_DEVICE", "cpu"),
            tesseract_lang=e.get("BIUMIND_TESSERACT_LANG", "eng+chi_sim"),
        )
