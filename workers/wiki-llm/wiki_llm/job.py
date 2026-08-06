"""Wire payloads exchanged with brain over NATS.

Two shapes:

    IngestRequest   brain → worker  (subject: brain.wiki.ingest.requested)
    Update          worker → brain  (subject: brain.wiki.ingest.update)

Both round-trip JSON; from_payload validates required fields and rejects
shape mismatches with ValueError so the runner can log + drop without
crashing the subscription.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass, field, asdict
from typing import Any, Optional


def _require_uuid(payload: dict, key: str) -> str:
    v = payload.get(key)
    if not isinstance(v, str) or not v:
        raise ValueError(f"missing or non-string {key}")
    try:
        uuid.UUID(v)
    except (TypeError, ValueError) as e:
        raise ValueError(f"{key} is not a valid UUID: {v!r}") from e
    return v


def _optional_uuid(payload: dict, key: str) -> Optional[str]:
    v = payload.get(key)
    if v is None or v == "":
        return None
    if not isinstance(v, str):
        raise ValueError(f"{key} must be a string UUID")
    try:
        uuid.UUID(v)
    except ValueError as e:
        raise ValueError(f"{key} is not a valid UUID: {v!r}") from e
    return v


@dataclass(frozen=True)
class IngestRequest:
    """Inbound: a task brain wants this worker to fulfill."""

    task_id: str
    project_id: str
    owner_id: str
    title: str
    raw_text: str
    source_id: Optional[str]

    @classmethod
    def from_payload(cls, payload: Any) -> "IngestRequest":
        if not isinstance(payload, dict):
            raise ValueError("payload must be a JSON object")
        task_id = _require_uuid(payload, "task_id")
        project_id = _require_uuid(payload, "project_id")
        owner_id = _require_uuid(payload, "owner_id")
        source_id = _optional_uuid(payload, "source_id")
        raw_text = payload.get("raw_text", "") or ""
        if not isinstance(raw_text, str):
            raise ValueError("raw_text must be a string")
        if source_id is None and not raw_text.strip():
            raise ValueError("either source_id or raw_text is required")
        title = payload.get("title", "") or ""
        if not isinstance(title, str):
            raise ValueError("title must be a string")
        return cls(
            task_id=task_id, project_id=project_id, owner_id=owner_id,
            title=title, raw_text=raw_text, source_id=source_id,
        )


# ─── Outbound updates ──────────────────────────────────────────

# Update kinds — kept as constants so brain's subscriber and this worker
# can validate against the same set without import-time coupling.
KIND_RUNNING = "running"
KIND_PAGE = "page"
KIND_DONE = "done"
KIND_FAILED = "failed"
KIND_CANCELLED = "cancelled"


@dataclass(frozen=True)
class Update:
    """Outbound status / progress event for one task.

    `kind=running`   marks task started (worker accepted job)
    `kind=page`      one wiki page just landed; payload carries
                     path/title/content/index. The brain subscriber is
                     the one who creates the page row and assigns
                     ``page_id``; the worker doesn't need to know.
    `kind=done`      terminal: all pages emitted successfully
    `kind=failed`    terminal: error string in `error`
    `kind=cancelled` terminal: worker observed cancel signal at boundary
    """

    task_id: str
    kind: str
    path: Optional[str] = None
    title: Optional[str] = None
    content: Optional[str] = None
    index: Optional[int] = None
    error: Optional[str] = None
    progress: dict = field(default_factory=dict)

    def to_payload(self) -> dict:
        # Drop None fields so the wire is minimal — brain's subscriber
        # treats absent keys the same as nulls but the trace logs are
        # cleaner.
        d = asdict(self)
        return {k: v for k, v in d.items() if v is not None and v != {}}
