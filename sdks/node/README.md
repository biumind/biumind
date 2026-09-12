# @biumind/sdk

Node.js client for [BiuMind Agentics](https://biumind.ai).

Zero runtime dependencies — uses Node 18+'s built-in `fetch`.

## Install

```bash
npm install @biumind/sdk
```

## Usage

```js
import { BiuMindConfig, HubClient, MemoryClient } from "@biumind/sdk";

const cfg = BiuMindConfig.fromEnv(); // BIUMIND_HUB_URL + BIUMIND_TOKEN (+ BIUMIND_BRAIN_URL, BIUMIND_TIMEOUT_MS)

const hub = new HubClient(cfg);
for await (const chunk of hub.messagesStream({
  model: "claude-sonnet-4-6",
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
  await hub.messages({ model: "...", messages: [...] });
} catch (e) {
  if (e instanceof RateLimitError) {
    await new Promise((r) => setTimeout(r, e.retryAfter * 1000 || 1000));
  } else if (e instanceof AuthError) {
    /* refresh token */
  } else throw e;
}
```

完整 API（含 `rawStream`、`list`、`delete`、错误类型全表、三语言行为差异）见
[开发者文档：SDK](https://biumind.ai/docs/developers/sdks/)。
