"""MemoryClient — Brain memory service.

Mirrors services/brain/internal/memory/api/api.go contract:

    POST   /v1/memory                 store
    GET    /v1/memory                 list (filter by project_id, kind)
    GET    /v1/memory/recall          hybrid recall
    DELETE /v1/memory/{id}            delete
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from typing import List, Optional

from .config import BiuMindConfig
from ._http import request_json


@dataclass(frozen=True)
class Memory:
    id: str
    project_id: str
    kind: str
    content: str
    salience: float
    created_at: datetime
    last_accessed_at: datetime
    score: Optional[float] = None  # only set on recall responses

    @classmethod
    def from_json(cls, j: dict) -> "Memory":
        return cls(
            id=j["id"],
            project_id=j.get("project_id", ""),
            kind=j.get("kind", "recall"),
            content=j.get("content", ""),
            salience=float(j.get("salience", 0.5)),
            created_at=_parse_ts(j.get("created_at")),
            last_accessed_at=_parse_ts(j.get("last_accessed_at")),
            score=(float(j["score"]) if "score" in j else None),
        )


@dataclass(frozen=True)
class RecallResult:
    memories: List[Memory]
    mode: str   # "hybrid" | "lexical" | "unknown"
    query: str

    @classmethod
    def from_json(cls, j: dict) -> "RecallResult":
        return cls(
            memories=[Memory.from_json(m) for m in j.get("memories", [])],
            mode=j.get("mode", "unknown"),
            query=j.get("query", ""),
        )


_VALID_KINDS = {"recall", "preference", "skill"}


class MemoryClient:
    def __init__(self, config: BiuMindConfig) -> None:
        self._cfg = config

    def store(
        self,
        *,
        project_id: str,
        content: str,
        kind: str = "recall",
        salience: Optional[float] = None,
    ) -> Memory:
        if kind not in _VALID_KINDS:
            raise ValueError(f"invalid kind {kind!r}; expected one of {_VALID_KINDS}")
        body = {"project_id": project_id, "kind": kind, "content": content}
        if salience is not None:
            body["salience"] = salience
        _, payload = request_json(
            "POST",
            f"{self._cfg.brain_url}/v1/memory",
            token=self._cfg.token,
            timeout=self._cfg.timeout,
            body=body,
        )
        return Memory.from_json(payload)

    def list(
        self,
        *,
        project_id: str,
        kind: Optional[str] = None,
        limit: int = 100,
    ) -> List[Memory]:
        query = {"project_id": project_id, "limit": str(limit)}
        if kind:
            query["kind"] = kind
        _, payload = request_json(
            "GET",
            f"{self._cfg.brain_url}/v1/memory",
            token=self._cfg.token,
            timeout=self._cfg.timeout,
            query=query,
        )
        return [Memory.from_json(m) for m in payload.get("memories", [])]

    def recall(
        self,
        *,
        project_id: str,
        q: str,
        kind: Optional[str] = None,
        limit: int = 10,
    ) -> RecallResult:
        if not q.strip():
            raise ValueError("q (query) is required")
        query = {"project_id": project_id, "q": q, "limit": str(limit)}
        if kind:
            query["kind"] = kind
        _, payload = request_json(
            "GET",
            f"{self._cfg.brain_url}/v1/memory/recall",
            token=self._cfg.token,
            timeout=self._cfg.timeout,
            query=query,
        )
        return RecallResult.from_json(payload)

    def delete(self, id: str) -> None:
        request_json(
            "DELETE",
            f"{self._cfg.brain_url}/v1/memory/{id}",
            token=self._cfg.token,
            timeout=self._cfg.timeout,
        )


def _parse_ts(raw) -> datetime:
    if not raw:
        return datetime.fromtimestamp(0, tz=timezone.utc)
    try:
        # Go emits RFC3339 with "Z" suffix; fromisoformat needs "+00:00".
        if isinstance(raw, str) and raw.endswith("Z"):
            raw = raw[:-1] + "+00:00"
        return datetime.fromisoformat(raw)
    except (TypeError, ValueError):
        return datetime.fromtimestamp(0, tz=timezone.utc)
