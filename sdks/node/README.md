# @biumind/sdk

Node.js client for [BiuMind Agentics](https://biumind.com).

Zero runtime dependencies — uses Node 18+'s built-in `fetch`.

## Install

```bash
npm install @biumind/sdk
```

## Usage

```js
import { BiuMindConfig, RelayClient, MemoryClient } from "@biumind/sdk";

const cfg = BiuMindConfig.fromEnv(); // BIUMIND_MODEL_RELAY_URL + BIUMIND_TOKEN

const relay = new RelayClient(cfg);
for await (const chunk of relay.messagesStream({
  model: "claude-3-5-sonnet-latest",
  messages: [{ role: "user", content: "Why is the sky blue?" }],
})) {
  process.stdout.write(chunk);
}

const mem = new MemoryClient(cfg);
await mem.store({ projectId: "proj_x", content: "user prefers dark mode" });
const r = await mem.recall({ projectId: "proj_x", q: "ui preference" });
for (const m of r.memories) console.log(m.score, m.content);
```

## Errors

```js
import { RateLimitError, AuthError } from "@biumind/sdk";

try {
  await relay.messages({ model: "...", messages: [...] });
} catch (e) {
  if (e instanceof RateLimitError) {
    await new Promise((r) => setTimeout(r, e.retryAfter * 1000 || 1000));
  } else if (e instanceof AuthError) {
    /* refresh token */
  } else throw e;
}
```
