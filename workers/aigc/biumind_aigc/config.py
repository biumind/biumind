"""Worker configuration loaded from env vars.

环境变量:
    BIUMIND_NATS_URL / NATS_URL    NATS 地址 (default nats://localhost:4222)
    BIUMIND_ENV                    环境 slug (default "dev")
    BIUMIND_AIGC_QUEUE             queue group name (default "aigc-py")
    BIUMIND_AIGC_TIMEOUT_S         单任务总超时 (default 600)
    BIUMIND_AIGC_POLL_INTERVAL_S   上游轮询间隔 (default 3.0)

    DASHSCOPE_API_KEY              通义万相 key (空时拒收 dashscope 任务)
    VOLCENGINE_ARK_API_KEY         火山豆包 Ark API key (空时拒收 volcengine 任务)

    AIGC_GENERATE_VIA_RELAY        段3.6: true 时生成走 model-relay 单一 egress
                                   (provider 在 relay 支持名单内才生效); 默认 false 直连上游
    AIGC_MODEL_RELAY_URL           model-relay 基址 (e.g. http://model-relay:7001)
    IDENTITY_INTERNAL_TOKEN        调 model-relay /v1/internal/* 的共享 bearer

    AIGC_S3_ENDPOINT               MinIO endpoint (e.g. http://minio:9000)
    AIGC_S3_ACCESS_KEY / SECRET    MinIO 凭据
    AIGC_S3_REGION                 default us-east-1

各 bucket 名按 storage design §4 默认值 (biumind-aigc-{uploads,outputs,...}).
可由 AIGC_BUCKET_OUTPUTS 等单独覆盖.
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
    poll_interval_s: float

    # provider keys
    dashscope_api_key: str
    volcengine_ark_api_key: str

    # 段3.6: model-relay 单一 egress
    generate_via_relay: bool
    model_relay_url: str
    model_relay_internal_token: str

    # 爆款解析 (hotparse): STT + LLM 拆解所用的 model-relay model_code。
    # 经 /v1/internal/transcribe + /v1/internal/chat 调用。task.params 可覆盖。
    hotparse_stt_model: str
    hotparse_llm_model: str

    # MinIO
    s3_endpoint: str
    s3_access_key: str
    s3_secret_key: str
    s3_region: str

    bucket_uploads: str
    bucket_outputs: str
    bucket_derivatives: str
    bucket_public: str
    bucket_temp: str

    # ─── derived subjects ─────────────────────────────

    @property
    def submit_subject(self) -> str:
        # 服务端发, worker 订阅
        return "aigc.task.submit"

    @property
    def update_subject(self) -> str:
        # worker 发, services/aigc orchestrator 订阅
        return "aigc.task.update"

    @property
    def cancel_subject(self) -> str:
        return "aigc.task.cancel"

    # ─── factory ──────────────────────────────────────

    @classmethod
    def from_env(cls, env: dict | None = None) -> "Config":
        e = env if env is not None else os.environ
        return cls(
            nats_url=e.get("BIUMIND_NATS_URL") or e.get("NATS_URL") or "nats://localhost:4222",
            env=e.get("BIUMIND_ENV", "dev"),
            queue_group=e.get("BIUMIND_AIGC_QUEUE", "aigc-py"),
            timeout_s=int(e.get("BIUMIND_AIGC_TIMEOUT_S", "600")),
            poll_interval_s=float(e.get("BIUMIND_AIGC_POLL_INTERVAL_S", "3.0")),
            dashscope_api_key=e.get("DASHSCOPE_API_KEY", ""),
            volcengine_ark_api_key=e.get("VOLCENGINE_ARK_API_KEY", ""),
            # 段3.6 后默认 true:生成统一经 model-relay(直连 provider 已删)。
            generate_via_relay=(e.get("AIGC_GENERATE_VIA_RELAY", "true") or "true").lower()
            in ("1", "true", "yes", "on"),
            model_relay_url=e.get("AIGC_MODEL_RELAY_URL") or e.get("MODEL_RELAY_URL") or "",
            model_relay_internal_token=e.get("IDENTITY_INTERNAL_TOKEN", ""),
            hotparse_stt_model=e.get("AIGC_HOTPARSE_STT_MODEL", "whisper-1"),
            hotparse_llm_model=e.get("AIGC_HOTPARSE_LLM_MODEL", "claude-opus-4-8"),
            s3_endpoint=e.get("AIGC_S3_ENDPOINT", ""),
            s3_access_key=e.get("AIGC_S3_ACCESS_KEY", ""),
            s3_secret_key=e.get("AIGC_S3_SECRET_KEY", ""),
            s3_region=e.get("AIGC_S3_REGION", "us-east-1"),
            bucket_uploads=e.get("AIGC_BUCKET_UPLOADS", "biumind-aigc-uploads"),
            bucket_outputs=e.get("AIGC_BUCKET_OUTPUTS", "biumind-aigc-outputs"),
            bucket_derivatives=e.get("AIGC_BUCKET_DERIVATIVES", "biumind-aigc-derivatives"),
            bucket_public=e.get("AIGC_BUCKET_PUBLIC", "biumind-aigc-public"),
            bucket_temp=e.get("AIGC_BUCKET_TEMP", "biumind-aigc-temp"),
        )
