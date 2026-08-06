"""Wire-format Job models.

Both schemas are JSON-encoded NATS messages.

BinaryJob (input subject: brain.ingest.binary):

    {
      "source_id":  "<uuid>",
      "project_id": "<uuid>",
      "user_id":    "<uuid>",
      "kind":       "pdf" | "image" | "audio",
      "title":      "optional",
      "url":        "optional",
      "data_b64":   "<base64 raw bytes>",
      "metadata":   { "language": "zh", ... }   # optional
    }

TextJob (output subject: brain.ingest.requested) — matches the Go
``services/brain/internal/ingestbus.Job`` shape so the existing consumer
keeps working unchanged.
"""

from __future__ import annotations

import base64
from dataclasses import dataclass, field
from typing import Any, Dict, Optional


SUPPORTED_BINARY_KINDS = {"pdf", "image", "audio"}


@dataclass
class BinaryJob:
    source_id: str
    project_id: str
    user_id: str
    kind: str
    data: bytes
    title: str = ""
    url: str = ""
    metadata: Dict[str, str] = field(default_factory=dict)

    @classmethod
    def from_payload(cls, payload: Dict[str, Any]) -> "BinaryJob":
        kind = payload.get("kind", "")
        if kind not in SUPPORTED_BINARY_KINDS:
            raise ValueError(
                f"unsupported kind {kind!r}; expected one of {sorted(SUPPORTED_BINARY_KINDS)}"
            )
        sid = payload.get("source_id") or ""
        pid = payload.get("project_id") or ""
        uid = payload.get("user_id") or ""
        if not (sid and pid and uid):
            raise ValueError("source_id / project_id / user_id are required")

        raw_b64 = payload.get("data_b64", "")
        if not raw_b64:
            raise ValueError("data_b64 is required (binary blobs only)")
        try:
            data = base64.b64decode(raw_b64, validate=True)
        except Exception as e:  # noqa: BLE001 — surface to caller as ValueError
            raise ValueError(f"data_b64 is not valid base64: {e}") from e

        return cls(
            source_id=sid,
            project_id=pid,
            user_id=uid,
            kind=kind,
            data=data,
            title=payload.get("title") or "",
            url=payload.get("url") or "",
            metadata=dict(payload.get("metadata") or {}),
        )


@dataclass
class TextJob:
    """Matches services/brain/internal/ingestbus.Job — re-published after
    extraction so the Go pipeline does the two-step CoT and Wiki write."""

    source_id: str
    project_id: str
    user_id: str
    kind: str  # always "plain" downstream — let Brain decide structure
    url: str
    title: str
    content: str

    def to_payload(self) -> Dict[str, Any]:
        return {
            "source_id": self.source_id,
            "project_id": self.project_id,
            "user_id": self.user_id,
            "kind": self.kind,
            "url": self.url,
            "title": self.title,
            "content": self.content,
        }

    @classmethod
    def from_extracted(cls, job: BinaryJob, text: str,
                       title_override: Optional[str] = None) -> "TextJob":
        title = title_override or job.title
        if not title:
            # Synthesize a title from the source kind so Brain's two-step
            # CoT has something to anchor on. Operators can override
            # via Job.title.
            title = f"[{job.kind}] {job.source_id}"
        return cls(
            source_id=job.source_id,
            project_id=job.project_id,
            user_id=job.user_id,
            kind="plain",
            url=job.url,
            title=title,
            content=text,
        )
