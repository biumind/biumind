"""Cancel-signal registry fed by brain's ingest cancel broadcast.

Brain's cancel API (``POST .../ingest/tasks/{tid}/cancel``) does two
things: set ``cancel_requested_at`` on the row, and publish a
fire-and-forget broadcast on ``biumind.<env>.brain.wiki.ingest.cancel``
carrying ``{"task_id": ...}``. Every wiki-llm worker instance subscribes
to that subject (no queue group — it is a broadcast, not work dispatch)
and records the task id here. The runner consults the registry twice:

  1. On task pickup — a task cancelled while still queued is rejected
     with a ``cancelled`` update instead of burning an LLM call
  2. At every stream chunk boundary — an in-flight task aborts the LLM
     stream and reports ``cancelled``; pages already emitted stay saved
     (streaming partial-save semantics)

The broadcast is fire-and-forget: if no worker was connected when it
fired, the signal is lost. That hole is closed brain-side by the
reaper's cancel sweep (tasks whose ``cancel_requested_at`` goes stale
are marked cancelled there), so this registry only needs to cover the
"worker was alive to hear it" window — a TTL bounds memory accordingly.
"""

from __future__ import annotations

import json
import time
from typing import Callable, Optional


class CancelRegistry:
    """Set of recently-cancelled task ids with TTL-based expiry."""

    def __init__(self, ttl_s: float = 3600.0,
                 clock: Callable[[], float] = time.monotonic) -> None:
        self._ttl = ttl_s
        self._clock = clock
        self._seen: dict[str, float] = {}

    def add(self, task_id: str) -> None:
        self._seen[task_id] = self._clock()
        self._prune()

    def is_cancelled(self, task_id: str) -> bool:
        ts = self._seen.get(task_id)
        if ts is None:
            return False
        if self._clock() - ts > self._ttl:
            del self._seen[task_id]
            return False
        return True

    def _prune(self) -> None:
        # Amortized bound: only rebuild once the map grows past a batch
        # of tasks; steady-state ingest volume stays far below this.
        if len(self._seen) < 1024:
            return
        cutoff = self._clock() - self._ttl
        self._seen = {k: v for k, v in self._seen.items() if v >= cutoff}


def parse_cancel_task_id(raw: bytes) -> Optional[str]:
    """Decode one cancel-broadcast message → task_id, or None on junk.

    Accepts both the bare payload and brain's BusPublisher envelope
    (``{topic, kind, payload}``), same tolerance as the request path.
    """
    try:
        payload = json.loads(raw.decode("utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError):
        return None
    if isinstance(payload, dict) and isinstance(payload.get("payload"), dict):
        payload = payload["payload"]
    if not isinstance(payload, dict):
        return None
    tid = payload.get("task_id")
    return tid if isinstance(tid, str) and tid else None
