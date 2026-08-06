"""HTTP client for brain's internal worker-only endpoints.

Currently only one endpoint:

    GET /v1/internal/wiki/sources/{source_id}?owner_id={uuid}
    Header: X-Biumind-Internal-Token: <shared secret>

Returns the source row (kind/url/title/raw/metadata/sha256/status).
The worker uses this to resolve source_id-only ingest tasks — when a
task arrives with no inline raw_text, the worker fetches `raw` here
and uses that as the LLM input. Avoids inlining potentially-large
clipped page bodies into NATS messages, which has both throughput
(JetStream message size limits) and cost (NATS bandwidth in cluster
deployments) implications.

Why a separate module from llm.py: that one talks to hub via the
public Anthropic-shape API; this one talks to brain via a
shared-secret private API. Different trust model, different auth
pattern, different error class — keeping them apart prevents accidental
credential leakage across surfaces.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Optional

import httpx


logger = logging.getLogger("biumind.wiki_llm.brain")


class BrainClientError(RuntimeError):
    """Raised on transport / status / payload errors when calling brain."""


@dataclass(frozen=True)
class BrainConfig:
    base_url: str
    internal_token: str
    timeout_s: float = 10.0


@dataclass(frozen=True)
class SourceRow:
    """Subset of brain.sources the worker actually consumes."""
    id: str
    project_id: str
    owner_id: str
    title: str
    raw: str
    kind: str
    url: str

    @classmethod
    def from_payload(cls, p: dict) -> "SourceRow":
        # We don't validate every field strictly — brain is the only
        # producer and the only field we *need* is `raw`. Everything
        # else is extra context for logging.
        return cls(
            id=str(p.get("id", "")),
            project_id=str(p.get("project_id", "")),
            owner_id=str(p.get("owner_id", "")),
            title=str(p.get("title", "") or ""),
            raw=str(p.get("raw", "") or ""),
            kind=str(p.get("kind", "") or ""),
            url=str(p.get("url", "") or ""),
        )


async def fetch_source(
    cfg: BrainConfig,
    *,
    source_id: str,
    owner_id: str,
) -> Optional[SourceRow]:
    """GET one source row by id. Returns None on 404; raises on other errors.

    The worker treats 404 the same as "source disappeared between task
    creation and worker pickup" — task is failed gracefully rather than
    crashing the consumer loop.
    """
    if not cfg.base_url:
        raise BrainClientError("brain base_url missing — set BIUMIND_BRAIN_URL")
    if not cfg.internal_token:
        raise BrainClientError(
            "brain internal token missing — "
            "set BIUMIND_INTERNAL_TOKEN to match the brain env",
        )
    url = (
        cfg.base_url.rstrip("/")
        + f"/v1/internal/wiki/sources/{source_id}"
    )
    headers = {"X-Biumind-Internal-Token": cfg.internal_token}
    params = {"owner_id": owner_id}

    timeout = httpx.Timeout(connect=5.0, read=cfg.timeout_s,
                            write=5.0, pool=5.0)
    async with httpx.AsyncClient(timeout=timeout) as client:
        try:
            resp = await client.get(url, headers=headers, params=params)
        except httpx.HTTPError as e:
            raise BrainClientError(f"brain request failed: {e}") from e

    if resp.status_code == 404:
        return None
    if resp.status_code == 401:
        raise BrainClientError(
            "brain rejected internal token — "
            "BIUMIND_INTERNAL_TOKEN may be out of sync with brain env",
        )
    if resp.status_code >= 400:
        raise BrainClientError(
            f"brain returned HTTP {resp.status_code}: "
            f"{resp.text[:300]}",
        )
    try:
        return SourceRow.from_payload(resp.json())
    except (ValueError, TypeError) as e:
        raise BrainClientError(f"bad source payload: {e}") from e
