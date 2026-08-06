"""Shared configuration for HubClient and MemoryClient.

Environment variables:

    BIUMIND_HUB_URL    — base URL for Hub (e.g. https://hub.biumind.com)
    BIUMIND_BRAIN_URL  — base URL for Brain (defaults to BIUMIND_HUB_URL)
    BIUMIND_TOKEN      — bearer JWT
    BIUMIND_TIMEOUT    — request timeout in seconds (default 30)

``from_env`` builds the config from these. Pass overrides explicitly to
the constructor for tests / multi-tenant usage.
"""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class BiuMindConfig:
    """Connection settings shared by every client.

    ``hub_url`` is required. ``brain_url`` defaults to ``hub_url`` because
    the bundled deployment exposes both behind one ingress. Override when
    Brain is on a separate host.
    """

    hub_url: str
    token: str
    brain_url: str = ""
    timeout: float = 30.0

    def __post_init__(self) -> None:
        # Normalize trailing slashes so request building is unambiguous.
        object.__setattr__(self, "hub_url", self.hub_url.rstrip("/"))
        if self.brain_url:
            object.__setattr__(self, "brain_url", self.brain_url.rstrip("/"))
        else:
            object.__setattr__(self, "brain_url", self.hub_url)

    @classmethod
    def from_env(cls) -> "BiuMindConfig":
        hub = os.environ.get("BIUMIND_HUB_URL", "").strip()
        token = os.environ.get("BIUMIND_TOKEN", "").strip()
        if not hub:
            raise ValueError("BIUMIND_HUB_URL is required")
        if not token:
            raise ValueError("BIUMIND_TOKEN is required")
        return cls(
            hub_url=hub,
            token=token,
            brain_url=os.environ.get("BIUMIND_BRAIN_URL", "").strip(),
            timeout=float(os.environ.get("BIUMIND_TIMEOUT", "30")),
        )
