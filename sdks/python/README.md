# biumind (Python SDK)

Python client for [BiuMind Agentics](https://biumind.ai).

Stdlib-only — no third-party runtime dependencies.

## Install

```bash
pip install biumind
```

## Usage

```python
from biumind import BiuMindConfig, HubClient, MemoryClient

cfg = BiuMindConfig.from_env()  # BIUMIND_HUB_URL + BIUMIND_TOKEN (+ BIUMIND_BRAIN_URL, BIUMIND_TIMEOUT)

hub = HubClient(cfg)
for chunk in hub.messages_stream(
    model="claude-sonnet-4-6",
    messages=[{"role": "user", "content": "Why is the sky blue?"}],
):
    print(chunk, end="", flush=True)

mem = MemoryClient(cfg)
mem.store(project_id="proj_x", content="user prefers dark mode")
result = mem.recall(project_id="proj_x", q="ui preference")
for m in result.memories:
    print(m.score, m.content)
```

## Errors

```python
from biumind import RateLimitError, AuthError

try:
    hub.messages(model="...", messages=[...])
except RateLimitError as e:
    time.sleep(e.retry_after or 1)
except AuthError:
    refresh_token()
```

完整 API（含 `raw_stream`、`list`、`delete`、错误类型全表、三语言行为差异）见
[开发者文档：SDK](https://biumind.ai/docs/developers/sdks/)。
