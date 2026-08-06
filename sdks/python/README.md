# biumind (Python SDK)

Python client for [BiuMind Agentics](https://biumind.com).

Stdlib-only — no third-party runtime dependencies.

## Install

```bash
pip install biumind
```

## Usage

```python
from biumind import BiuMindConfig, RelayClient, MemoryClient

cfg = BiuMindConfig.from_env()  # BIUMIND_MODEL_RELAY_URL + BIUMIND_TOKEN

relay = RelayClient(cfg)
for chunk in relay.messages_stream(
    model="claude-3-5-sonnet-latest",
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
    relay.messages(model="...", messages=[...])
except RateLimitError as e:
    time.sleep(e.retry_after or 1)
except AuthError:
    refresh_token()
```
