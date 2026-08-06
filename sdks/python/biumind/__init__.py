"""BiuMind SDK — Python client for the BiuMind Agentics platform.

Two clients are exposed:

  - :class:`HubClient` — proxies the OpenAI / Anthropic-compatible
    relay (`POST /v1/messages`). Streams SSE deltas as a generator
    when ``stream=True``.

  - :class:`MemoryClient` — Brain memory service (`/v1/memory`,
    `/v1/memory/recall`, etc.).

Both share a common config (``BiuMindConfig``) and use ``urllib`` from
the stdlib so the package has zero non-stdlib runtime deps. That makes
``pip install biumind`` a zero-friction operation in air-gapped envs
and CI matrices.

Example:

    from biumind import BiuMindConfig, HubClient, MemoryClient

    cfg = BiuMindConfig.from_env()           # reads BIUMIND_HUB_URL etc.
    hub = HubClient(cfg)
    for chunk in hub.messages_stream(model="claude-3-5-sonnet",
                                     messages=[{"role":"user","content":"hi"}]):
        print(chunk, end="", flush=True)

    mem = MemoryClient(cfg)
    mem.store(project_id="proj_x", content="user prefers dark mode")
    for m in mem.recall(project_id="proj_x", q="ui preference").memories:
        print(m.content, m.score)
"""

from .config import BiuMindConfig
from .errors import BiuMindError, AuthError, RateLimitError, NotFoundError
from .hub import HubClient
from .memory import Memory, MemoryClient, RecallResult

__all__ = [
    "BiuMindConfig",
    "BiuMindError",
    "AuthError",
    "RateLimitError",
    "NotFoundError",
    "HubClient",
    "MemoryClient",
    "Memory",
    "RecallResult",
]

__version__ = "0.1.0"
