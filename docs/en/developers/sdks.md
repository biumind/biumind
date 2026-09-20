# Public Integration SDKs (Go / Python / Node)

The `sdks/` directory provides public integration SDKs in three languages (all **Apache-2.0** licensed, separate from the platform's BiuMind Community License):

| Language | Package | Source | Requirements |
|---|---|---|---|
| Go | `github.com/biumind/biumind/sdks/go` (package name `biumind`) | `sdks/go/` | Standard library only, Go 1.25+ |
| Python | `biumind` (0.1.0) | `sdks/python/` | Standard library only, Python 3.9+ |
| Node | `@biumind/sdk` (0.1.0) | `sdks/node/` | Built-in `fetch` only, Node 18+ |

The three SDKs cover two groups of capabilities:

- **Model calls**: via model-relay's Anthropic Messages-compatible endpoint (`POST /v1/messages`), streaming and non-streaming.
- **Memory storage**: via the Brain memory service (the `/v1/memory` family).

Authentication uniformly uses a Bearer JWT. Generate a PAT under "Settings → Virtual API Key" in the client and fill it into the Token field; see [Developers overview](index.md).

## When to use the SDKs, and when not to

- **Use the SDKs**: your code is already Go / Python / Node and you call model or memory APIs repeatedly — the SDKs handle the auth headers, timeouts, SSE stream parsing, and error type mapping for you.
- **Use REST**: other languages, or a one-off curl call — every endpoint is plain HTTP + JSON; just make requests directly. See [API reference](api.md) for endpoint details.
- **Use MCP**: you want an AI agent inside Claude Code or another MCP client to read and write your knowledge base and memory directly, with zero code. See [API reference: MCP](api.md).

> [!NOTE]
> The three SDKs deliberately keep their API surfaces isomorphic (one client mental model), but names and environment variables are not word-for-word identical — Go uses `RelayClient`, Python / Node use `HubClient`; the environment variable prefixes also differ (see each language's section below). When mixing languages, consult the [environment variable cross-reference](#environment-variable-cross-reference).

---

## Go SDK

### Installation

```bash
go get github.com/biumind/biumind/sdks/go
```

### Configuration

The `Config` struct (`sdks/go/biumind.go`):

```go
type Config struct {
    RelayURL string        // model-relay base URL, required
    BrainURL string        // Brain base URL; defaults to RelayURL when empty
    Token    string        // Bearer JWT (PAT)
    Timeout  time.Duration // defaults to 30 seconds when zero

    // HTTPClient can inject a custom http.Client; when nil the SDK builds one per the rules above
    HTTPClient *http.Client
}
```

Construct from environment variables:

```go
func LoadConfig() (Config, error)
```

Reads `BIUMIND_MODEL_RELAY_URL`, `BIUMIND_TOKEN`, `BIUMIND_BRAIN_URL` (optional). Missing either of the first two is an error. `Timeout` can only be set via the struct field; there is no corresponding environment variable.

```go
cfg, err := biumind.LoadConfig()
if err != nil {
    log.Fatal(err)
}
```

Or construct explicitly:

```go
cfg := biumind.Config{
    RelayURL: "https://biumind.ai",
    Token:    "bm_pat_...",
    Timeout:  60 * time.Second,
}
```

> [!WARNING]
> The Go SDK's model-gateway environment variable is `BIUMIND_MODEL_RELAY_URL`; the Python / Node SDKs use `BIUMIND_HUB_URL` — both point to the same model-relay service, but the names differ. Don't copy them blindly when porting scripts.

### RelayClient (model calls)

```go
func NewRelayClient(cfg Config) *RelayClient
```

Request body types:

```go
type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type MessagesRequest struct {
    Model     string                 `json:"model"`
    Messages  []Message              `json:"messages"`
    System    string                 `json:"system,omitempty"`
    MaxTokens int                    `json:"max_tokens,omitempty"`
    Stream    bool                   `json:"stream,omitempty"`   // set by the two methods themselves; no need to fill in
    Extra     map[string]interface{} `json:"-"`                  // extra vendor-specific fields, merged into the JSON last
}
```

#### Non-streaming: `Messages`

```go
func (h *RelayClient) Messages(ctx context.Context, req MessagesRequest) ([]byte, error)
```

Sends a non-streaming request and returns the **raw JSON bytes** — whether the upstream is Anthropic- or OpenAI-shaped is decided by server-side routing; the caller deserializes it.

```go
relay := biumind.NewRelayClient(cfg)
raw, err := relay.Messages(ctx, biumind.MessagesRequest{
    Model:     "claude-sonnet-4-6",
    MaxTokens: 1024,
    Messages:  []biumind.Message{{Role: "user", Content: "Why is the sky blue?"}},
})
if err != nil {
    log.Fatal(err)
}
var resp struct {
    Content []struct{ Text string `json:"text"` } `json:"content"`
}
_ = json.Unmarshal(raw, &resp)
fmt.Println(resp.Content[0].Text)
```

#### Streaming: `MessagesStream`

```go
func (h *RelayClient) MessagesStream(ctx context.Context, req MessagesRequest) (<-chan string, <-chan error)
```

Returns two channels: the data channel emits only the **text deltas** of Anthropic-style `content_block_delta` / `text_delta` events; errors that occur mid-stream go to the error channel, and the data channel is closed when the stream ends. Cancelling `ctx` interrupts the stream.

```go
chunks, errs := relay.MessagesStream(ctx, biumind.MessagesRequest{
    Model:    "claude-sonnet-4-6",
    Messages: []biumind.Message{{Role: "user", Content: "Why is the sky blue?"}},
})
for chunk := range chunks {
    fmt.Print(chunk)
}
if err := <-errs; err != nil {
    log.Fatal(err)
}
```

### MemoryClient (memory storage)

```go
func NewMemoryClient(cfg Config) *MemoryClient
```

Data types:

```go
type Memory struct {          // one memory record; Score is populated only in recall results
    ID             string
    ProjectID      string
    Kind           string    // "recall" | "preference" | "skill"
    Content        string
    Salience       float64   // salience, 0~1
    CreatedAt      time.Time
    LastAccessedAt time.Time
    Score          *float64
}

type RecallResult struct {
    Memories []Memory
    Mode     string   // "hybrid" | "lexical" | "unknown"
    Query    string
}
```

#### `Store` — write one memory

```go
func (m *MemoryClient) Store(ctx context.Context, projectID, content string, opts StoreOptions) (*Memory, error)
```

`StoreOptions{ Kind string; Salience *float64 }`. `Kind` defaults to `"recall"` when empty (the value must be `recall` / `preference` / `skill`, otherwise a local error is returned); when `Salience` is nil the server applies its default (currently 0.5).

```go
mem := biumind.NewMemoryClient(cfg)
saved, err := mem.Store(ctx, "proj_x", "The user prefers dark mode", biumind.StoreOptions{
    Kind:     "preference",
    Salience: ptr(0.8), // *float64
})
```

#### `List` — list recent memories

```go
func (m *MemoryClient) List(ctx context.Context, projectID string, opts ListOptions) ([]Memory, error)
```

`ListOptions{ Kind string; Limit int }`; `Limit` defaults to 100 when `<= 0`.

```go
items, _ := mem.List(ctx, "proj_x", biumind.ListOptions{Kind: "preference", Limit: 20})
```

#### `Recall` — hybrid retrieval

```go
func (m *MemoryClient) Recall(ctx context.Context, projectID, query string, opts RecallOptions) (*RecallResult, error)
```

Hybrid lexical + semantic retrieval over the project's memories. `RecallOptions{ Kind string; Limit int }`; `Limit` defaults to 10 when `<= 0`; an empty `query` is an error.

```go
r, _ := mem.Recall(ctx, "proj_x", "UI preferences", biumind.RecallOptions{})
for _, m := range r.Memories {
    fmt.Println(*m.Score, m.Content)
}
```

#### `Delete` — delete by id

```go
func (m *MemoryClient) Delete(ctx context.Context, id string) error
```

```go
_ = mem.Delete(ctx, "mem_123")
```

### Error handling

The Go SDK uses a single `*biumind.Error` type for all HTTP failures (non-HTTP failures such as network errors are plain errors):

```go
type Error struct {
    Status     int           // HTTP status code
    Body       string        // response body
    RetryAfter time.Duration // the server's Retry-After hint; 0 when absent
}

func (e *Error) IsAuth() bool       // 401 / 403
func (e *Error) IsRateLimit() bool  // 429
func (e *Error) IsNotFound() bool   // 404
```

```go
var be *biumind.Error
if errors.As(err, &be) {
    switch {
    case be.IsRateLimit():
        time.Sleep(be.RetryAfter)
    case be.IsAuth():
        // token expired or insufficient permissions; refresh credentials
    case be.IsNotFound():
        // resource does not exist
    }
}
```

---

## Python SDK

### Installation

```bash
pip install biumind
```

Zero third-party runtime dependencies (stdlib-only); can be installed directly in air-gapped environments.

### Configuration

```python
@dataclass(frozen=True)
class BiuMindConfig:
    hub_url: str          # required, model-relay base URL
    token: str            # required, Bearer JWT (PAT)
    brain_url: str = ""   # defaults to hub_url when empty
    timeout: float = 30.0 # seconds
```

Construct from environment variables:

```python
@classmethod
def from_env(cls) -> "BiuMindConfig"
```

Reads `BIUMIND_HUB_URL`, `BIUMIND_TOKEN`, `BIUMIND_BRAIN_URL` (optional), `BIUMIND_TIMEOUT` (optional, seconds, default 30). Missing either of the first two raises `ValueError`.

```python
from biumind import BiuMindConfig

cfg = BiuMindConfig.from_env()
# or construct explicitly:
cfg = BiuMindConfig(hub_url="https://biumind.ai", token="bm_pat_...", timeout=60.0)
```

### HubClient (model calls)

```python
from biumind import HubClient

hub = HubClient(cfg)
```

The server routes upstream based on the `model` field in the request (`claude-*` to Anthropic, `gpt-*` to OpenAI, etc.); the caller does not specify the vendor.

#### Non-streaming: `messages`

```python
def messages(
    self,
    *,
    model: str,
    messages: List[Dict[str, Any]],
    system: Optional[str] = None,
    max_tokens: int = 1024,
    extra: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]
```

Sends a non-streaming request and returns the **parsed full response dict**. Key/value pairs in `extra` are merged into the request body (for passing vendor-specific parameters).

```python
resp = hub.messages(
    model="claude-sonnet-4-6",
    messages=[{"role": "user", "content": "Why is the sky blue?"}],
)
print(resp["content"][0]["text"])
```

#### Streaming (text deltas only): `messages_stream`

```python
def messages_stream(
    self,
    *,
    model: str,
    messages: List[Dict[str, Any]],
    system: Optional[str] = None,
    max_tokens: int = 1024,
    extra: Optional[Dict[str, Any]] = None,
) -> Iterator[str]
```

A generator that yields the text deltas of `content_block_delta` / `text_delta` events one by one; other SSE event types are discarded.

```python
for chunk in hub.messages_stream(
    model="claude-sonnet-4-6",
    messages=[{"role": "user", "content": "Why is the sky blue?"}],
):
    print(chunk, end="", flush=True)
```

#### Streaming (full events): `raw_stream`

```python
def raw_stream(
    self,
    *,
    model: str,
    messages: List[Dict[str, Any]],
    system: Optional[str] = None,
    max_tokens: int = 1024,
    extra: Optional[Dict[str, Any]] = None,
) -> Iterator[Dict[str, Any]]
```

Same parameters as `messages_stream`, but yields every parsed full SSE event dict (including `message_start`, error events, etc.) — suited to callers who need usage statistics or tool-call events.

```python
for event in hub.raw_stream(model="claude-sonnet-4-6",
                            messages=[{"role": "user", "content": "hi"}]):
    if event.get("type") == "message_start":
        print(event)
```

### MemoryClient (memory storage)

```python
from biumind import MemoryClient

mem = MemoryClient(cfg)
```

Data types: `Memory` (fields `id` / `project_id` / `kind` / `content` / `salience` / `created_at` / `last_accessed_at` / `score`; times are parsed as `datetime`; `score` is populated only in recall results) and `RecallResult` (`memories` / `mode` / `query`).

#### `store` — write one memory

```python
def store(
    self,
    *,
    project_id: str,
    content: str,
    kind: str = "recall",
    salience: Optional[float] = None,
) -> Memory
```

`kind` must be `recall` / `preference` / `skill`, otherwise `ValueError` is raised; when `salience` is None the server applies its default (currently 0.5).

```python
saved = mem.store(project_id="proj_x", content="The user prefers dark mode", kind="preference")
```

#### `list` — list recent memories

```python
def list(
    self,
    *,
    project_id: str,
    kind: Optional[str] = None,
    limit: int = 100,
) -> List[Memory]
```

```python
items = mem.list(project_id="proj_x", kind="preference", limit=20)
```

#### `recall` — hybrid retrieval

```python
def recall(
    self,
    *,
    project_id: str,
    q: str,
    kind: Optional[str] = None,
    limit: int = 10,
) -> RecallResult
```

Hybrid lexical + semantic retrieval; a blank `q` raises `ValueError`.

```python
r = mem.recall(project_id="proj_x", q="UI preferences")
for m in r.memories:
    print(m.score, m.content)
```

#### `delete` — delete by id

```python
def delete(self, id: str) -> None
```

```python
mem.delete("mem_123")
```

### Error handling

A typed exception hierarchy, all inheriting from `BiuMindError` (carrying `status` and `body` attributes):

| Exception | Triggered by | Extra attributes |
|---|---|---|
| `AuthError` | 401 / 403 | — |
| `RateLimitError` | 429 | `retry_after` (seconds, float; 0.0 when the server gives no hint) |
| `NotFoundError` | 404 | — |
| `BiuMindError` | other 4xx / 5xx | — |

```python
import time
from biumind import RateLimitError, AuthError

try:
    hub.messages(model="claude-sonnet-4-6", messages=[{"role": "user", "content": "hi"}])
except RateLimitError as e:
    time.sleep(e.retry_after or 1)
except AuthError:
    ...  # refresh the token
```

---

## Node SDK

### Installation

```bash
npm install @biumind/sdk
```

Zero runtime dependencies; uses Node 18+'s built-in `fetch`. ESM (`"type": "module"`).

### Configuration

```js
export class BiuMindConfig {
  constructor({ hubUrl, token, brainUrl = "", timeoutMs = 30_000 })
  static fromEnv(env = process.env)
}
```

`fromEnv()` reads `BIUMIND_HUB_URL`, `BIUMIND_TOKEN`, `BIUMIND_BRAIN_URL` (optional), `BIUMIND_TIMEOUT_MS` (optional, milliseconds, default 30000). The constructor throws when `hubUrl` / `token` are missing.

```js
import { BiuMindConfig, HubClient, MemoryClient } from "@biumind/sdk";

const cfg = BiuMindConfig.fromEnv();
// or construct explicitly:
const cfg = new BiuMindConfig({ hubUrl: "https://biumind.ai", token: "bm_pat_..." });
```

### HubClient (model calls)

```js
const hub = new HubClient(cfg);
```

#### Non-streaming: `messages`

```js
async messages({ model, messages, system, maxTokens = 1024, extra })
```

Returns the parsed full response object. Key/value pairs in `extra` are merged into the request body.

```js
const resp = await hub.messages({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "Why is the sky blue?" }],
});
console.log(resp.content[0].text);
```

#### Streaming (text deltas only): `messagesStream`

```js
async *messagesStream({ model, messages, system, maxTokens = 1024, extra })
```

An async generator that yields only the `content_block_delta` / `text_delta` text deltas.

```js
for await (const chunk of hub.messagesStream({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "Why is the sky blue?" }],
})) {
  process.stdout.write(chunk);
}
```

#### Streaming (full events): `rawStream`

```js
async *rawStream({ model, messages, system, maxTokens = 1024, extra })
```

Yields every parsed full SSE event object.

```js
for await (const event of hub.rawStream({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "hi" }],
})) {
  if (event.type === "message_start") console.log(event);
}
```

### MemoryClient (memory storage)

```js
const mem = new MemoryClient(cfg);
```

> [!NOTE]
> The Node SDK's `Memory` object converts the server's snake_case fields to camelCase (`projectId` / `createdAt` / `lastAccessedAt`), unlike the Go / Python SDKs which keep the original field names. Times are `Date` objects (`null` when absent).

#### `store` — write one memory

```js
async store({ projectId, content, kind = "recall", salience })
```

`kind` must be `recall` / `preference` / `skill`, otherwise an error is thrown.

```js
const saved = await mem.store({ projectId: "proj_x", content: "The user prefers dark mode", kind: "preference" });
```

#### `list` — list recent memories

```js
async list({ projectId, kind, limit = 100 })
```

```js
const items = await mem.list({ projectId: "proj_x", kind: "preference", limit: 20 });
```

#### `recall` — hybrid retrieval

```js
async recall({ projectId, q, kind, limit = 10 })
```

Returns `{ memories, mode, query }`; a blank `q` throws.

```js
const r = await mem.recall({ projectId: "proj_x", q: "UI preferences" });
for (const m of r.memories) console.log(m.score, m.content);
```

#### `delete` — delete by id

```js
async delete(id)
```

```js
await mem.delete("mem_123");
```

### Error handling

A typed hierarchy isomorphic to the Python SDK's, all inheriting from `BiuMindError` (carrying `status` and `body` properties): `AuthError` (401 / 403), `RateLimitError` (429; `retryAfter` is in **seconds**), `NotFoundError` (404).

```js
import { RateLimitError, AuthError } from "@biumind/sdk";

try {
  await hub.messages({ model: "claude-sonnet-4-6", messages: [{ role: "user", content: "hi" }] });
} catch (e) {
  if (e instanceof RateLimitError) {
    await new Promise((r) => setTimeout(r, (e.retryAfter || 1) * 1000));
  } else if (e instanceof AuthError) {
    // refresh the token
  } else {
    throw e;
  }
}
```

---

## Environment variable cross-reference

| Purpose | Go | Python | Node |
|---|---|---|---|
| Model gateway address | `BIUMIND_MODEL_RELAY_URL` | `BIUMIND_HUB_URL` | `BIUMIND_HUB_URL` |
| Brain address (optional) | `BIUMIND_BRAIN_URL` | `BIUMIND_BRAIN_URL` | `BIUMIND_BRAIN_URL` |
| Credential | `BIUMIND_TOKEN` | `BIUMIND_TOKEN` | `BIUMIND_TOKEN` |
| Timeout (optional) | none (constructor field only) | `BIUMIND_TIMEOUT` (seconds) | `BIUMIND_TIMEOUT_MS` (milliseconds) |

## Behavioral differences across the three languages

| Dimension | Go | Python | Node |
|---|---|---|---|
| Client naming | `RelayClient` | `HubClient` | `HubClient` |
| Default timeout | 30 seconds | 30 seconds | 30000 milliseconds |
| Timeout semantics (non-streaming) | Covers the whole request (including reading the response body) | Covers each blocking socket operation | Covers only up to response-header arrival; reading the response body is unbounded |
| Timeout semantics (streaming) | **No timeout** (internally forces `Timeout = 0`; SSE is open-ended) | Covers each socket read (effectively an idle timeout) | Covers only up to response-header arrival; reading the stream is unbounded |
| Automatic retry | None | None | None |
| Non-streaming return | Raw `[]byte`; the caller deserializes | Parsed `dict` | Parsed object |
| Full SSE event stream | Not available | `raw_stream` | `rawStream` |
| `max_tokens` default | Field not sent (server decides) | 1024 | 1024 |
| Memory field naming | snake_case (`project_id`, etc.) | snake_case | camelCase (`projectId`, etc.) |
| Memory time type | `time.Time` | `datetime` (falls back to epoch on parse failure) | `Date` (`null` when absent) |
| Error on invalid `kind` | Returns an error | Raises `ValueError` | Throws a plain `Error` |

> [!WARNING]
> None of the three SDKs retries automatically: on a 429 rate limit, wait yourself (based on the error object's `RetryAfter` / `retry_after` / `retryAfter`, all in seconds) and resend.
>
> Go's streaming calls have no timeout protection — a long-hanging stream relies on the caller's `context.Context` for cancellation; in production, consider setting `cfg.HTTPClient` or using `context.WithTimeout` as a backstop.

## Licensing and boundaries

- `sdks/` is Apache-2.0 and freely usable in commercial integrations; the platform itself (`apps/`, `services/`, `workers/`, `packages/`) is under the BiuMind Community License — see [Developers overview](index.md).
- The SDKs cover only the two API groups: model calls and memory storage. Knowledge base, Wiki, graph, and the other capabilities go through the REST API — see [API reference](api.md).
