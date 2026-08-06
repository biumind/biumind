"""NATS parse job payload。

brain 在 upload 入库后 publish ``wiki.parse``/``requested``，payload 形如::

    {
      "source_id":  "<uuid>",
      "project_id": "<uuid>",
      "owner_id":   "<uuid>",
      "kind":       "upload",
      "mime":       "application/pdf",
      "filename":   "report.pdf"
    }

rescan 路径（tick 拉 parse-queue）复用同结构（brain_client.QueueItem.as_job）。
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class ParseJob:
    source_id: str
    project_id: str
    owner_id: str
    kind: str
    mime: str
    filename: str

    @classmethod
    def from_payload(cls, p: dict) -> "ParseJob":
        sid = str(p.get("source_id", "")).strip()
        pid = str(p.get("project_id", "")).strip()
        oid = str(p.get("owner_id", "")).strip()
        if not sid or not pid or not oid:
            raise ValueError(
                f"parse job missing required field: "
                f"source_id={sid!r} project_id={pid!r} owner_id={oid!r}"
            )
        return cls(
            source_id=sid,
            project_id=pid,
            owner_id=oid,
            kind=str(p.get("kind", "upload") or "upload"),
            mime=str(p.get("mime", "") or ""),
            filename=str(p.get("filename", "") or ""),
        )
