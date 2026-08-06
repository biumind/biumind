"""Event dataclasses — wire schema 对齐 services/aigc/internal/orchestrator.

SubmitTask 是 services/aigc 发出的; TaskUpdate 是 worker 发回的.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field, asdict
from typing import Any


@dataclass
class SubmitTask:
    """aigc.task.submit 消息 payload, 由 services/aigc/internal/api/generations.go 发出.

    P4.S1.3 起 payload 多 4 个 credential_* 字段, 由 model-relay envelope
    解密注入. 缺失时 (旧调用方 / dev fallback) credential_api_key 为空,
    worker 仍能从 cfg.*_api_key (env) 兜底.
    """

    task_id: str
    user_id: str
    type: str  # image | video | digital_human | hotparse
    model_code: str
    provider_code: str
    prompt: str
    cost_credits: int
    negative_prompt: str = ""
    params: dict[str, Any] = field(default_factory=dict)
    parent_sha: str = ""

    # P4.S1.3 / P4.S3.2 — Go 端注入的解密凭证. 空时退化到 cfg.*_api_key.
    credential_api_key: str = ""
    credential_base_url: str = ""
    credential_headers: dict[str, str] = field(default_factory=dict)
    credential_last4: str = ""

    # P4.S3.1 — model-relay /v1/jobs 注入. aigc /v1/generations 旧路径
    # 不带这个字段, worker 收到时为空, 沿用 aigc 自己的 hold/settle 流程.
    hold_id: str = ""

    @classmethod
    def from_json(cls, raw: bytes | str) -> "SubmitTask":
        if isinstance(raw, (bytes, bytearray)):
            raw = raw.decode("utf-8")
        d = json.loads(raw)
        # params 可能是 None / dict; 统一成 dict
        params = d.get("params") or {}
        headers = d.get("credential_headers") or {}
        return cls(
            task_id=d["task_id"],
            user_id=d["user_id"],
            type=d["type"],
            model_code=d["model_code"],
            provider_code=d["provider_code"],
            prompt=d.get("prompt", ""),
            cost_credits=int(d.get("cost_credits", 0)),
            negative_prompt=d.get("negative_prompt", ""),
            params=params if isinstance(params, dict) else {},
            parent_sha=d.get("parent_sha", ""),
            credential_api_key=d.get("credential_api_key", "") or "",
            credential_base_url=d.get("credential_base_url", "") or "",
            credential_headers=headers if isinstance(headers, dict) else {},
            credential_last4=d.get("credential_last4", "") or "",
            hold_id=d.get("hold_id", "") or "",
        )

    def has_payload_credential(self) -> bool:
        """True iff 上游 (model-relay 或 aigc P4.S1.3+) 注入了明文 key."""
        return bool(self.credential_api_key)


@dataclass
class OutputEntry:
    """完成时一次性带回的 output 元数据."""

    idx: int
    kind: str  # image | video | audio | cover
    sha256: str
    storage_url: str
    storage_key: str
    blurhash: str = ""
    cover_sha: str = ""
    mime_type: str = ""
    file_size: int = 0
    width: int = 0
    height: int = 0
    duration_ms: int = 0
    # 结构化产物 (爆款解析: 文案/钩子/分镜/标签)。kind="hotparse" 时填充,
    # 透传到 services/aigc orchestrator → task_outputs.metadata jsonb。
    # 普通 image/video output 留空 {}, orchestrator 端按空值忽略。
    metadata: dict = field(default_factory=dict)


@dataclass
class TaskUpdate:
    """aigc.task.update 消息 payload, 由 worker 发往 services/aigc orchestrator.

    字段对齐 services/aigc/internal/orchestrator/orchestrator.go 的 TaskUpdateEvent.
    """

    task_id: str
    status: str  # queued | running | completed | failed | blocked | cancelled
    progress: int = 0
    outputs: list[OutputEntry] = field(default_factory=list)
    error_code: str = ""
    error_message: str = ""
    refunded_credits: int = 0
    external_task_id: str = ""
    cache_hit: bool = False

    def to_json(self) -> bytes:
        # asdict 处理嵌套 dataclass; 空字符串/0 字段也保留 (orchestrator 已用 omitempty 兜底)
        return json.dumps(asdict(self), ensure_ascii=False).encode("utf-8")
