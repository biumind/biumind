# 公共集成 SDK（Go / Python / Node）

`sdks/` 目录提供三个语言的公共集成 SDK（均为 **Apache-2.0** 许可，与平台本体的 BiuMind Community License 分离）：

| 语言 | 包名 | 源码位置 | 依赖要求 |
|---|---|---|---|
| Go | `github.com/biumind/biumind/sdks/go`（包名 `biumind`） | `sdks/go/` | 仅标准库，Go 1.25+ |
| Python | `biumind`（0.1.0） | `sdks/python/` | 仅标准库，Python 3.9+ |
| Node | `@biumind/sdk`（0.1.0） | `sdks/node/` | 仅内置 `fetch`，Node 18+ |

三个 SDK 覆盖两组能力：

- **模型调用**：走 model-relay 的 Anthropic Messages 兼容端点（`POST /v1/messages`），支持流式与非流式。
- **记忆存取**：走 Brain 记忆服务（`/v1/memory` 系列）。

认证统一使用 Bearer JWT。在客户端「设置 → 虚拟 API Key」生成 PAT 后填入 Token 字段，详见 [开发者总览](index.md)。

## 什么时候用 SDK，什么时候不用

- **用 SDK**：你的代码本身是 Go / Python / Node，且需要反复调用模型或记忆接口——SDK 替你处理了认证头、超时、SSE 流式解析和错误类型映射。
- **用 REST**：其他语言，或只需要一次性 curl 调用——所有端点都是普通 HTTP + JSON，直接请求即可，接口细节见 [API 参考](api.md)。
- **用 MCP**：想让 Claude Code 等 MCP 客户端里的 AI Agent 直接读写你的知识库与记忆，零代码接入，见 [API 参考：MCP](api.md)。

> [!NOTE]
> 三个 SDK 的 API 面刻意保持同构（同一个客户端心智模型），但命名与环境变量并非逐字一致——Go 用 `RelayClient`，Python / Node 用 `HubClient`；环境变量前缀也有差异（见下文各语言小节）。混用多语言时请对照 [环境变量对照表](#环境变量对照表)。

---

## Go SDK

### 安装

```bash
go get github.com/biumind/biumind/sdks/go
```

### 配置

`Config` 结构体（`sdks/go/biumind.go`）：

```go
type Config struct {
    RelayURL string        // model-relay 基地址，必填
    BrainURL string        // Brain 基地址，为空时默认等于 RelayURL
    Token    string        // Bearer JWT（PAT）
    Timeout  time.Duration // 为零时默认 30 秒

    // HTTPClient 可注入自定义 http.Client；为 nil 时 SDK 按上述规则自建
    HTTPClient *http.Client
}
```

从环境变量构造：

```go
func LoadConfig() (Config, error)
```

读取 `BIUMIND_MODEL_RELAY_URL`、`BIUMIND_TOKEN`、`BIUMIND_BRAIN_URL`（可选）。前两个缺失时报错。`Timeout` 只能通过构造参数设置，没有对应环境变量。

```go
cfg, err := biumind.LoadConfig()
if err != nil {
    log.Fatal(err)
}
```

或显式构造：

```go
cfg := biumind.Config{
    RelayURL: "https://biumind.ai",
    Token:    "bm_pat_...",
    Timeout:  60 * time.Second,
}
```

> [!WARNING]
> Go SDK 的模型网关环境变量名是 `BIUMIND_MODEL_RELAY_URL`，Python / Node SDK 用的是 `BIUMIND_HUB_URL`——两者指向同一个 model-relay 服务，但名字不同，迁移脚本时别照抄。

### RelayClient（模型调用）

```go
func NewRelayClient(cfg Config) *RelayClient
```

请求体类型：

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
    Stream    bool                   `json:"stream,omitempty"`   // 两个方法会自行设置，无需手填
    Extra     map[string]interface{} `json:"-"`                  // 追加的供应商专属字段，最后合并进 JSON
}
```

#### 非流式：`Messages`

```go
func (h *RelayClient) Messages(ctx context.Context, req MessagesRequest) ([]byte, error)
```

发送非流式请求，返回**原始 JSON 字节**——上游是 Anthropic 还是 OpenAI 形状由服务端路由决定，调用方自行反序列化。

```go
relay := biumind.NewRelayClient(cfg)
raw, err := relay.Messages(ctx, biumind.MessagesRequest{
    Model:     "claude-sonnet-4-6",
    MaxTokens: 1024,
    Messages:  []biumind.Message{{Role: "user", Content: "为什么天空是蓝色的？"}},
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

#### 流式：`MessagesStream`

```go
func (h *RelayClient) MessagesStream(ctx context.Context, req MessagesRequest) (<-chan string, <-chan error)
```

返回两个 channel：数据 channel 只发出 Anthropic 风格 `content_block_delta` / `text_delta` 的**文本增量**；流中发生的错误走错误 channel，数据 channel 在流结束时关闭。`ctx` 取消会中断流。

```go
chunks, errs := relay.MessagesStream(ctx, biumind.MessagesRequest{
    Model:    "claude-sonnet-4-6",
    Messages: []biumind.Message{{Role: "user", Content: "为什么天空是蓝色的？"}},
})
for chunk := range chunks {
    fmt.Print(chunk)
}
if err := <-errs; err != nil {
    log.Fatal(err)
}
```

### MemoryClient（记忆存取）

```go
func NewMemoryClient(cfg Config) *MemoryClient
```

数据类型：

```go
type Memory struct {          // 单条记忆记录；Score 仅在 recall 结果中填充
    ID             string
    ProjectID      string
    Kind           string    // "recall" | "preference" | "skill"
    Content        string
    Salience       float64   // 显著度 0~1
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

#### `Store` — 写入一条记忆

```go
func (m *MemoryClient) Store(ctx context.Context, projectID, content string, opts StoreOptions) (*Memory, error)
```

`StoreOptions{ Kind string; Salience *float64 }`。`Kind` 为空时默认 `"recall"`（取值必须是 `recall` / `preference` / `skill`，否则本地报错）；`Salience` 为 nil 时由服务端取默认值（当前 0.5）。

```go
mem := biumind.NewMemoryClient(cfg)
saved, err := mem.Store(ctx, "proj_x", "用户偏好深色模式", biumind.StoreOptions{
    Kind:     "preference",
    Salience: ptr(0.8), // *float64
})
```

#### `List` — 列出最近记忆

```go
func (m *MemoryClient) List(ctx context.Context, projectID string, opts ListOptions) ([]Memory, error)
```

`ListOptions{ Kind string; Limit int }`，`Limit <= 0` 时默认 100。

```go
items, _ := mem.List(ctx, "proj_x", biumind.ListOptions{Kind: "preference", Limit: 20})
```

#### `Recall` — 混合检索

```go
func (m *MemoryClient) Recall(ctx context.Context, projectID, query string, opts RecallOptions) (*RecallResult, error)
```

对项目记忆做词法 + 语义混合检索。`RecallOptions{ Kind string; Limit int }`，`Limit <= 0` 时默认 10；`query` 为空时报错。

```go
r, _ := mem.Recall(ctx, "proj_x", "界面偏好", biumind.RecallOptions{})
for _, m := range r.Memories {
    fmt.Println(*m.Score, m.Content)
}
```

#### `Delete` — 按 id 删除

```go
func (m *MemoryClient) Delete(ctx context.Context, id string) error
```

```go
_ = mem.Delete(ctx, "mem_123")
```

### 错误处理

Go SDK 用单一 `*biumind.Error` 类型承载全部 HTTP 失败（网络错误等非 HTTP 失败是普通 error）：

```go
type Error struct {
    Status     int           // HTTP 状态码
    Body       string        // 响应体
    RetryAfter time.Duration // 服务端 Retry-After 提示，无则为 0
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
        // token 过期或权限不足，刷新凭证
    case be.IsNotFound():
        // 资源不存在
    }
}
```

---

## Python SDK

### 安装

```bash
pip install biumind
```

零第三方运行时依赖（stdlib-only），可在无外网环境直接安装。

### 配置

```python
@dataclass(frozen=True)
class BiuMindConfig:
    hub_url: str          # 必填，model-relay 基地址
    token: str            # 必填，Bearer JWT（PAT）
    brain_url: str = ""   # 为空时默认等于 hub_url
    timeout: float = 30.0 # 秒
```

从环境变量构造：

```python
@classmethod
def from_env(cls) -> "BiuMindConfig"
```

读取 `BIUMIND_HUB_URL`、`BIUMIND_TOKEN`、`BIUMIND_BRAIN_URL`（可选）、`BIUMIND_TIMEOUT`（可选，秒，默认 30）。前两个缺失时抛 `ValueError`。

```python
from biumind import BiuMindConfig

cfg = BiuMindConfig.from_env()
# 或显式构造：
cfg = BiuMindConfig(hub_url="https://biumind.ai", token="bm_pat_...", timeout=60.0)
```

### HubClient（模型调用）

```python
from biumind import HubClient

hub = HubClient(cfg)
```

服务端按请求里的 `model` 字段路由上游（`claude-*` 去 Anthropic、`gpt-*` 去 OpenAI 等），调用方不用指定供应商。

#### 非流式：`messages`

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

发送非流式请求，返回**解析后的完整响应 dict**。`extra` 里的键值会合并进请求体（传供应商专属参数用）。

```python
resp = hub.messages(
    model="claude-sonnet-4-6",
    messages=[{"role": "user", "content": "为什么天空是蓝色的？"}],
)
print(resp["content"][0]["text"])
```

#### 流式（仅文本增量）：`messages_stream`

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

生成器，逐个产出 `content_block_delta` / `text_delta` 的文本增量；其余 SSE 事件类型被丢弃。

```python
for chunk in hub.messages_stream(
    model="claude-sonnet-4-6",
    messages=[{"role": "user", "content": "为什么天空是蓝色的？"}],
):
    print(chunk, end="", flush=True)
```

#### 流式（完整事件）：`raw_stream`

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

与 `messages_stream` 同参，但产出每个解析后的完整 SSE 事件 dict（含 `message_start`、错误事件等），适合需要 usage 统计或工具调用事件的调用方。

```python
for event in hub.raw_stream(model="claude-sonnet-4-6",
                            messages=[{"role": "user", "content": "hi"}]):
    if event.get("type") == "message_start":
        print(event)
```

### MemoryClient（记忆存取）

```python
from biumind import MemoryClient

mem = MemoryClient(cfg)
```

数据类型：`Memory`（字段 `id` / `project_id` / `kind` / `content` / `salience` / `created_at` / `last_accessed_at` / `score`，时间解析为 `datetime`，`score` 仅 recall 结果填充）与 `RecallResult`（`memories` / `mode` / `query`）。

#### `store` — 写入一条记忆

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

`kind` 必须是 `recall` / `preference` / `skill`，否则抛 `ValueError`；`salience` 为 None 时服务端取默认值（当前 0.5）。

```python
saved = mem.store(project_id="proj_x", content="用户偏好深色模式", kind="preference")
```

#### `list` — 列出最近记忆

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

#### `recall` — 混合检索

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

词法 + 语义混合检索；`q` 为空白时抛 `ValueError`。

```python
r = mem.recall(project_id="proj_x", q="界面偏好")
for m in r.memories:
    print(m.score, m.content)
```

#### `delete` — 按 id 删除

```python
def delete(self, id: str) -> None
```

```python
mem.delete("mem_123")
```

### 错误处理

类型化异常层级，全部继承 `BiuMindError`（带 `status` 与 `body` 属性）：

| 异常 | 触发 | 额外属性 |
|---|---|---|
| `AuthError` | 401 / 403 | — |
| `RateLimitError` | 429 | `retry_after`（秒，float，服务端未提示时为 0.0） |
| `NotFoundError` | 404 | — |
| `BiuMindError` | 其余 4xx / 5xx | — |

```python
import time
from biumind import RateLimitError, AuthError

try:
    hub.messages(model="claude-sonnet-4-6", messages=[{"role": "user", "content": "hi"}])
except RateLimitError as e:
    time.sleep(e.retry_after or 1)
except AuthError:
    ...  # 刷新 token
```

---

## Node SDK

### 安装

```bash
npm install @biumind/sdk
```

零运行时依赖，使用 Node 18+ 内置 `fetch`。ESM（`"type": "module"`）。

### 配置

```js
export class BiuMindConfig {
  constructor({ hubUrl, token, brainUrl = "", timeoutMs = 30_000 })
  static fromEnv(env = process.env)
}
```

`fromEnv()` 读取 `BIUMIND_HUB_URL`、`BIUMIND_TOKEN`、`BIUMIND_BRAIN_URL`（可选）、`BIUMIND_TIMEOUT_MS`（可选，毫秒，默认 30000）。`hubUrl` / `token` 缺失时构造器抛错。

```js
import { BiuMindConfig, HubClient, MemoryClient } from "@biumind/sdk";

const cfg = BiuMindConfig.fromEnv();
// 或显式构造：
const cfg = new BiuMindConfig({ hubUrl: "https://biumind.ai", token: "bm_pat_..." });
```

### HubClient（模型调用）

```js
const hub = new HubClient(cfg);
```

#### 非流式：`messages`

```js
async messages({ model, messages, system, maxTokens = 1024, extra })
```

返回解析后的完整响应对象。`extra` 的键值合并进请求体。

```js
const resp = await hub.messages({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "为什么天空是蓝色的？" }],
});
console.log(resp.content[0].text);
```

#### 流式（仅文本增量）：`messagesStream`

```js
async *messagesStream({ model, messages, system, maxTokens = 1024, extra })
```

异步生成器，只产出 `content_block_delta` / `text_delta` 文本增量。

```js
for await (const chunk of hub.messagesStream({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "为什么天空是蓝色的？" }],
})) {
  process.stdout.write(chunk);
}
```

#### 流式（完整事件）：`rawStream`

```js
async *rawStream({ model, messages, system, maxTokens = 1024, extra })
```

产出每个解析后的完整 SSE 事件对象。

```js
for await (const event of hub.rawStream({
  model: "claude-sonnet-4-6",
  messages: [{ role: "user", content: "hi" }],
})) {
  if (event.type === "message_start") console.log(event);
}
```

### MemoryClient（记忆存取）

```js
const mem = new MemoryClient(cfg);
```

> [!NOTE]
> Node SDK 的 `Memory` 对象把服务端 snake_case 字段转成了 camelCase（`projectId` / `createdAt` / `lastAccessedAt`），与 Go / Python SDK 保持原始字段名不同。时间是 `Date` 对象（缺失时为 `null`）。

#### `store` — 写入一条记忆

```js
async store({ projectId, content, kind = "recall", salience })
```

`kind` 必须是 `recall` / `preference` / `skill`，否则抛错。

```js
const saved = await mem.store({ projectId: "proj_x", content: "用户偏好深色模式", kind: "preference" });
```

#### `list` — 列出最近记忆

```js
async list({ projectId, kind, limit = 100 })
```

```js
const items = await mem.list({ projectId: "proj_x", kind: "preference", limit: 20 });
```

#### `recall` — 混合检索

```js
async recall({ projectId, q, kind, limit = 10 })
```

返回 `{ memories, mode, query }`；`q` 为空白时抛错。

```js
const r = await mem.recall({ projectId: "proj_x", q: "界面偏好" });
for (const m of r.memories) console.log(m.score, m.content);
```

#### `delete` — 按 id 删除

```js
async delete(id)
```

```js
await mem.delete("mem_123");
```

### 错误处理

与 Python SDK 同构的类型层级，全部继承 `BiuMindError`（带 `status` 与 `body` 属性）：`AuthError`（401 / 403）、`RateLimitError`（429，`retryAfter` 单位为**秒**）、`NotFoundError`（404）。

```js
import { RateLimitError, AuthError } from "@biumind/sdk";

try {
  await hub.messages({ model: "claude-sonnet-4-6", messages: [{ role: "user", content: "hi" }] });
} catch (e) {
  if (e instanceof RateLimitError) {
    await new Promise((r) => setTimeout(r, (e.retryAfter || 1) * 1000));
  } else if (e instanceof AuthError) {
    // 刷新 token
  } else {
    throw e;
  }
}
```

---

## 环境变量对照表

| 用途 | Go | Python | Node |
|---|---|---|---|
| 模型网关地址 | `BIUMIND_MODEL_RELAY_URL` | `BIUMIND_HUB_URL` | `BIUMIND_HUB_URL` |
| Brain 地址（可选） | `BIUMIND_BRAIN_URL` | `BIUMIND_BRAIN_URL` | `BIUMIND_BRAIN_URL` |
| 凭证 | `BIUMIND_TOKEN` | `BIUMIND_TOKEN` | `BIUMIND_TOKEN` |
| 超时（可选） | 无（仅构造参数） | `BIUMIND_TIMEOUT`（秒） | `BIUMIND_TIMEOUT_MS`（毫秒） |

## 三语言行为差异

| 维度 | Go | Python | Node |
|---|---|---|---|
| 客户端命名 | `RelayClient` | `HubClient` | `HubClient` |
| 默认超时 | 30 秒 | 30 秒 | 30000 毫秒 |
| 超时语义（非流式） | 覆盖整个请求（含读响应体） | 覆盖每次 socket 阻塞操作 | 仅覆盖到响应头到达，读响应体阶段不受限 |
| 超时语义（流式） | **不超时**（内部强制 `Timeout = 0`，SSE 开放式） | 覆盖每次 socket 读（相当于空闲超时） | 仅覆盖到响应头到达，流式读取阶段不受限 |
| 自动重试 | 无 | 无 | 无 |
| 非流式返回 | 原始 `[]byte`，调用方自行反序列化 | 解析后的 `dict` | 解析后的对象 |
| 完整 SSE 事件流 | 无此能力 | `raw_stream` | `rawStream` |
| `max_tokens` 默认 | 不发送该字段（由服务端决定） | 1024 | 1024 |
| Memory 字段命名 | snake_case（`project_id` 等） | snake_case | camelCase（`projectId` 等） |
| Memory 时间类型 | `time.Time` | `datetime`（解析失败回退到 epoch） | `Date`（缺失为 `null`） |
| 无效 `kind` 的报错 | 返回 error | 抛 `ValueError` | 抛普通 `Error` |

> [!WARNING]
> 三个 SDK 都**没有自动重试**：429 限流时要根据错误对象里的 `RetryAfter` / `retry_after` / `retryAfter`（均为秒）自行等待后重发。
>
> Go 的流式调用没有超时保护——长时间挂着的流依赖调用方传入的 `context.Context` 取消；生产环境建议设置 `cfg.HTTPClient` 或用 `context.WithTimeout` 兜底。

## 许可与边界

- `sdks/` 为 Apache-2.0，可自由用于商业集成；平台本体（`apps/` `services/` `workers/` `packages/`）是 BiuMind Community License，见 [开发者总览](index.md)。
- SDK 只覆盖模型调用与记忆存取两组接口；知识库、Wiki、图谱等其余能力走 REST API，见 [API 参考](api.md)。
