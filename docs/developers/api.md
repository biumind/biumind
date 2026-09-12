# API 参考（MCP + REST）

BiuMind 对外提供两种集成方式：

- **MCP（Model Context Protocol）**——推荐。一个端点把知识库读写、记忆、检索、问答全部暴露为 AI 客户端可直接调用的 tools。
- **REST API**——按模块分组的常规 HTTP 接口，适合脚本、CI 与服务端集成。

两种方式共用同一套认证（Bearer token）。

## 基础信息

| 项 | 值 |
|----|----|
| 云端 Base URL | `https://biumind.ai` |
| 自托管 / 本地 Base URL | `http://localhost:8088`（compose 栈默认入口） |
| 请求 / 响应格式 | JSON（`Content-Type: application/json`） |
| 认证 | `Authorization: Bearer <token>`（详见[认证](#认证)） |

下文所有示例中的 `$BASE` 指 Base URL，`$TOKEN` 指你的 Bearer token。

统一错误形状（所有 REST 端点一致）：

```json
{
  "error": { "code": "not_found", "message": "" }
}
```

常见状态码：`400` 参数错误、`401` 缺失 / 无效 token、`403` 无权限（资源不属于当前用户）、`404` 不存在、`409` 版本冲突（带 `If-Match` 的写操作）、`429` 限流。

> [!NOTE]
> 管理端接口（`/v1/admin/*`）与内部服务间接口（`/v1/internal/*`）不对外提供，本文档不覆盖。WebSocket（SDK Protocol）、SSE 实时通知等流式通道见 [SDK Protocol](./sdk-protocol.md)。

---

## 认证

所有业务端点都要求 Bearer token。BiuMind 支持三种获取方式，第三方集成推荐使用 **API Token（PAT）**。

### 方式一（推荐）：API Token（PAT）

PAT 是长效的编程访问 token，格式为 `bm_<8位前缀>_<JWT>`，默认有效期 1 年。整个 token 字符串原样放进 `Authorization` 头即可，服务端会自动识别 `bm_` 前缀并按 JWT 验签——所有业务端点（REST 与 MCP）都直接可用。

**在客户端生成**：打开 BiuMind 客户端 → 设置 → **API Tokens** 页签，创建后 token 明文**只显示一次**，请立即保存。

**通过 API 生成**（需要先用账号密码登录拿一个 JWT，PAT 不能再造 PAT）：

```bash
# 创建 PAT（name 必填）
curl -X POST "$BASE/v1/identity/me/tokens" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-integration",
    "scopes": [],
    "ttl_seconds": 0
  }'
```

请求字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 必填。展示名 |
| `scopes` | string[] | 可选。作用域标签 |
| `workspace_id` / `project_id` | string (UUID) | 可选。作用域限定 |
| `ttl_seconds` | int | 可选。有效期秒数；`0` 或缺省 = 1 年（只能缩短，不能加长） |

响应 `201`：

```json
{
  "id": "…",
  "name": "my-integration",
  "prefix": "a1b2c3d4",
  "redacted": "bm_a1b2c3d4_…",
  "scopes": [],
  "expires_at": "2027-09-10T00:00:00Z",
  "created_at": "2026-09-10T00:00:00Z",
  "secret": "bm_a1b2c3d4_eyJhbGciOi…"
}
```

`secret` 即完整 token，**只在创建响应里返回一次**，服务端不再保存明文。

配套管理端点：

| 方法 + 路径 | 说明 |
|------|------|
| `GET /v1/identity/me/tokens` | 列出我的 PAT（不含 secret，只显示 `redacted` 前缀） |
| `DELETE /v1/identity/me/tokens/{id}` | 吊销一个 PAT，响应 `202` |
| `GET /v1/identity/whoami` | 校验任意 token：返回 `user_id` / `roles` / `plan` / `scope` / `expires_at` 等 |

> [!WARNING]
> PAT 等同于账号的完整操作权限，请像保管密码一样保管；泄露后立即在客户端或通过 `DELETE /v1/identity/me/tokens/{id}` 吊销。PAT 不能用于再创建 PAT（防止权限链式扩散）。

### 方式二：账号密码登录换取 JWT

适合自己控制完整登录流程的场景。access token 有效期较短（响应里的 `expires_in_seconds`），过期后用 refresh token 换新。

```bash
curl -X POST "$BASE/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{ "email": "you@example.com", "password": "…", "device_name": "my-script" }'
```

响应 `200`：

```json
{
  "access_token": "eyJhbGciOi…",
  "refresh_token": "…",
  "expires_in_seconds": 86400,
  "user": { "id": "…", "email": "…", "display_name": "…", "email_verified": true }
}
```

刷新（旋转式：返回新 access + 新 refresh，旧 refresh 同时作废）：

```bash
curl -X POST "$BASE/v1/auth/refresh" \
  -H "Content-Type: application/json" \
  -d '{ "refresh_token": "…" }'
```

> [!NOTE]
> 未完成邮箱验证的账号登录会返回 `403 email_not_verified`，不会签发 token。

### 方式三：OAuth 2.1

BiuMind 同时是一个 OAuth 2.1 授权服务器（主要服务 CLI 浏览器登录，也可用于第三方应用）。端点：

| 路径 | 说明 |
|------|------|
| `GET /.well-known/oauth-authorization-server` | RFC 8414 服务器元数据（端点发现入口） |
| `GET /oauth/authorize` | 授权端点 |
| `POST /oauth/token` | 令牌端点 |
| `POST /oauth/revoke` | 令牌吊销 |
| `POST /oauth/register` | 动态客户端注册 |

从元数据端点开始发现即可，本文不展开。

---

## MCP 接入（推荐）

MCP 服务器把知识库（Wiki）、记忆（Memory）与检索能力打包成一组 tools，供任何支持 MCP 的 AI 客户端或 Agent 直接调用。

### 端点与协议

| 项 | 值 |
|----|----|
| 端点 | `POST /v1/mcp`（固定路径，无子路径、无会话） |
| 认证 | `Authorization: Bearer <JWT 或 PAT>`，每个请求都校验 |
| 协议 | JSON-RPC 2.0 over HTTP；MCP 协议版本 `2025-03-26` |
| 服务端信息 | `serverInfo`：`biumind-brain` / `0.2.0` |
| 请求大小上限 | 1 MB |

传输形态说明：每个 `POST /v1/mcp` 携带一个 JSON-RPC 请求，返回一个 JSON 响应。**没有 SSE 流、没有长会话**——需要流式或本地进程形态的场景见下文 [stdio 传输](#stdio-传输自托管)。

支持的方法：

| 方法 | 说明 |
|------|------|
| `initialize` | 握手，返回协议版本、`serverInfo` 与 `capabilities` |
| `tools/list` | 返回全部 tool 定义（含 JSON Schema 参数） |
| `tools/call` | 调用一个 tool（`params.name` + `params.arguments`） |
| `ping` | 存活探测，返回 `{}` |

JSON-RPC 错误码沿用协议保留值：`-32700` 解析失败、`-32600` 非法请求（含认证失败）、`-32601` 方法 / tool 不存在、`-32602` 参数错误、`-32603` 内部错误。

tool 结果统一为 MCP 标准信封：`content`（人类可读文本数组）+ `structuredContent`（机器可读结构化数据，各 tool 的返回字段见下表）+ `isError`。

### 握手示例

```bash
curl -X POST "$BASE/v1/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {}
  }'
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-03-26",
    "serverInfo": { "name": "biumind-brain", "version": "0.2.0" },
    "capabilities": { "tools": { "listChanged": false } }
  }
}
```

### Tools 一览

所有 tools 都按当前 token 的用户做数据隔离（只能访问该用户拥有的项目）。`project_id` 均为 UUID，可用 `wiki.list_projects` 发现。

#### 记忆（Memory）

| Tool | 参数 | 返回（structuredContent） |
|------|------|------|
| `memory.store` | `project_id`*、`content`*、`kind`（`recall`/`preference`/`habit`，默认 `recall`）、`salience`（0–1，默认 0.5） | `memory`（含 `id`） |
| `memory.list` | `project_id`*、`kind`、`limit`（1–500，默认 100） | `memories` |
| `memory.recall` | `project_id`*、`query`*、`kind`、`limit`（1–50，默认 10） | `memories`（每条带 `score`）、`mode`（`lexical` 或 `hybrid`）、`query` |
| `memory.delete` | `id`*（记忆 UUID） | `deleted` |

#### 知识库（Wiki）

| Tool | 参数 | 返回（structuredContent） |
|------|------|------|
| `wiki.list_projects` | `limit`（1–500，默认 100） | `projects`（`id` / `name` / `created_at`）。**先调这个拿 project_id** |
| `wiki.search` | `query`*、`project_id`（缺省 = 跨当前用户全部项目）、`limit`（1–100，默认 20） | `hits`（`kind` / `page_id` / `project_id` / `title` / `snippet` / `score`）、`mode`（`bm25` 或 `hybrid`） |
| `wiki.list_pages` | `project_id`*、`limit`（1–500，默认 100） | `pages` |
| `wiki.get_page` | `page_id`*、`include_blocks`（默认 true） | `page`（含 `body_md` / `frontmatter` / `version`；blocks 上限 200 条） |
| `wiki.create_page` | `project_id`*、`title`*、`parent_id`、`frontmatter`（object） | `page` |
| `wiki.update_page` | `page_id`*、`title`、`frontmatter`、`version`（乐观锁，传 `get_page` 拿到的 `version`；缺省 = 强制覆盖） | `page`；版本冲突时报错并附 `current_version` |
| `wiki.ingest` | `project_id`*、`raw_text`*（Markdown / 纯文本）、`title` | `task`（异步摄取任务，LLM 自动拆页建库） |
| `wiki.list_reviews` | `project_id`*、`kind`（`dedup`/`lint`/`sweep`/`merge`/`suggestion`/`contradiction`）、`status`（默认 `open`）、`limit` | `reviews`、`status` |
| `wiki.dismiss_review` | `id`*（review UUID） | `id`、`status` |
| `wiki.merge_pages` | `canonical_id`*（保留页）、`duplicate_id`*（被并入页，软删除） | `canonical_id`、`duplicate_id`、`merged` |
| `wiki.related_pages` | `page_id`*、`limit`（1–100，默认 20） | `related`（`page_id` / `title` / `score` / `signals`） |
| `wiki.chat` | `project_id`*、`message`*、`mode`（`fast`/`standard`/`deep`，默认 `standard`）、`model`（缺省用平台默认对话模型） | `answer`、`cited_pages`、`model`、`mode`、`prompt_tokens`、`completion_tokens` |

`*` = 必填。

> [!NOTE]
> `wiki.chat` 在服务端跑一个只读的 LLM 问答循环（检索项目内页面后作答并引用来源），按正常模型调用计费。`fast`/`standard`/`deep` 对应递增的检索与迭代预算。

> [!WARNING]
> 个别 tools 依赖部署侧可选组件（向量检索、摄取队列、问答模型等）。未配置时 `tools/list` 仍会列出该 tool，但调用会返回内部错误帧——这是设计行为，保证 tool 列表在不同部署间稳定。

### 调用示例

```bash
curl -X POST "$BASE/v1/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "wiki.search",
      "arguments": { "query": "agent 架构", "limit": 5 }
    }
  }'
```

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [ { "type": "text", "text": "5 hits for \"agent 架构\" (mode=hybrid)" } ],
    "structuredContent": {
      "hits": [
        {
          "id": "wiki:page:…",
          "kind": "page",
          "page_id": "…",
          "project_id": "…",
          "title": "Agent 架构",
          "snippet": "…",
          "score": 0.019,
          "updated_at": "2026-09-01T00:00:00Z"
        }
      ],
      "mode": "hybrid",
      "query": "agent 架构"
    },
    "isError": false
  }
}
```

### stdio 传输（自托管）

仓库提供独立的 stdio MCP 服务器（JSON-RPC 2.0 over 标准输入输出，同一套 tools），供本地 AI 客户端以子进程方式拉起。从源码构建：

```bash
go build -o biu-memory-mcp ./services/brain/cmd/memory-mcp
```

最小环境变量：

```json
{
  "mcpServers": {
    "biumind-memory": {
      "command": "/usr/local/bin/biu-memory-mcp",
      "env": {
        "DATABASE_URL": "postgres://…",
        "MEMORY_MCP_USER_ID": "<你的用户 UUID>",
        "MEMORY_MCP_PROJECT_ID": "<项目 UUID>"
      }
    }
  }
}
```

可选配置：`EMBED_PROVIDER` / `EMBED_BASE_URL` / `EMBED_API_KEY` / `EMBED_MODEL` / `EMBED_DIMS`（启用语义检索），NATS 地址（启用 `wiki.ingest`）。

> [!WARNING]
> stdio 传输通过环境变量固定用户身份，**不做 JWT 校验**——能启动该进程的本地进程即可以该用户身份操作。仅适合本机单用户场景；多租户 / 云端请走 HTTP 传输（`POST /v1/mcp`）。

---

## REST API

以下端点均要求 `Authorization: Bearer <JWT 或 PAT>`，除特别标注"公开"外。所有写接口按资源归属做隔离：访问不属于自己的项目返回 `404`/`403`。

带版本控制的写操作（更新页面 / 块）支持乐观并发：`GET` 响应带 `ETag`（版本号），写入时带 `If-Match: <版本号>` 请求头；版本不匹配返回 `409` 并附 `server_version` / `server_payload`。

### 知识库（Wiki）

#### 项目

**创建项目**

```bash
curl -X POST "$BASE/v1/wiki/projects" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "name": "我的知识库" }'
```

`POST /v1/wiki/projects`，body：`name`*（项目名）、`template_id`（可选模板）。响应 `200`：项目对象（`id` / `name` / `created_at` / `template_id`）。

**列出项目**

`GET /v1/wiki/projects` → `{"projects": [...]}`（最多 100 条）。

#### 页面

| 方法 + 路径 | 说明 |
|------|------|
| `POST /v1/wiki/projects/{pid}/pages` | 创建页面。body：`title`*、`parent_id`（可选父页面，构成树） |
| `GET /v1/wiki/projects/{pid}/pages` | 列出项目内页面（最多 200 条） |
| `GET /v1/wiki/projects/{pid}/pages/{id}` | 页面详情，响应带 `ETag` |
| `PUT /v1/wiki/projects/{pid}/pages/{id}` | 更新标题 / frontmatter。body：`title`、`frontmatter`（object）；可带 `If-Match` |
| `PUT /v1/wiki/projects/{pid}/pages/{id}/body` | 整篇写入 Markdown 正文。body：`{"body_md": "…"}`；可带 `If-Match`。服务端会自动重算内容块投影 |
| `DELETE /v1/wiki/projects/{pid}/pages/{id}` | 软删除页面 |

页面对象：

```json
{
  "id": "…",
  "project_id": "…",
  "title": "页面标题",
  "frontmatter": {},
  "body_md": "# Markdown 正文",
  "share_mode": "private",
  "version": 3,
  "created_at": "2026-09-01T00:00:00Z",
  "updated_at": "2026-09-10T00:00:00Z"
}
```

写入正文的完整示例：

```bash
curl -X PUT "$BASE/v1/wiki/projects/$PID/pages/$PAGE_ID/body" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "If-Match: 3" \
  -d '{ "body_md": "# 修改后的正文\n\n新的内容。" }'
```

#### 内容块（Blocks）

页面正文的结构化投影，可单独读写：

| 方法 + 路径 | 说明 |
|------|------|
| `GET /v1/wiki/projects/{pid}/pages/{id}/blocks` | 列出页面全部块 |
| `POST /v1/wiki/projects/{pid}/pages/{id}/blocks` | 创建块。body：`position`（数字排序位）、`type`（默认 `text`）、`content`（object） |
| `PUT /v1/wiki/projects/{pid}/blocks/{id}` | 更新块。body：`content`、`position`；可带 `If-Match` |
| `DELETE /v1/wiki/projects/{pid}/blocks/{id}` | 软删除块 |

#### 增量变更

| 方法 + 路径 | 说明 |
|------|------|
| `GET /v1/wiki/projects/{pid}/changes?since={id}&limit=200` | 拉取 `since` 之后的项目事件流（`page.updated` / `block.deleted` 等），用于轮询同步。`limit` 上限 1000 |

### 搜索

**统一搜索**（跨知识库 / 网页 / 笔记的多路融合检索）：

```bash
curl -X POST "$BASE/v1/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "query": "向量检索", "scope": "wiki", "limit": 20 }'
```

`POST /v1/search`，body：

| 字段 | 说明 |
|------|------|
| `query`* | 搜索词 |
| `scope` | `wiki`（默认，页面 + 块）/ `web`（外部网络）/ `all`（融合） |
| `project_id` | 可选，限定单个项目 |
| `limit` | 1–100，默认 20 |
| `include_notes` | `true` 时把个人笔记纳入检索（默认 `false`） |

响应按来源分组（`wiki` / `web` / `vector` / `graph` / `notes` / `images`）并附 `fused` 融合排序结果；`wiki` 命中含 `page_id` / `title` / `snippet` / `score`。

### 图谱（Graph）

| 方法 + 路径 | 说明 |
|------|------|
| `GET /v1/graph/projects/{pid}/nodes?q=&kind=&limit=` | 列出 / 搜索图谱节点（实体） |
| `GET /v1/graph/projects/{pid}/nodes/{id}` | 节点详情 + 一跳边（`edges`）+ 反向引用（`backlinks`） |
| `GET /v1/graph/projects/{pid}/related?node_id=&depth=2&relations=&limit=` | 从种子节点 BFS 扩展邻居；`depth` 默认 2，`relations` 逗号分隔过滤 |
| `POST /v1/graph/projects/{pid}/extract` | 对一个内容块手动触发实体抽取。body：`block_id`、`content`（object）；返回抽取并 upsert 的节点 |

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/graph/projects/$PID/related?node_id=$NODE_ID&depth=2"
```

响应：`{"neighbors": [{…节点字段, "depth": 1, "relation": "…"}]}`。

### 记忆（Memory）

四个端点，均要求目标 `project_id` 属于当前用户：

```bash
# 存一条记忆
curl -X POST "$BASE/v1/memory" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "project_id": "$PID",
    "kind": "preference",
    "content": "用户偏好简洁的中文回复",
    "salience": 0.8
  }'
```

| 方法 + 路径 | 说明 |
|------|------|
| `POST /v1/memory` | body：`project_id`*、`content`*、`kind`（`recall`/`preference`/`habit`）、`salience`（0–1）。响应：记忆对象 |
| `GET /v1/memory?project_id=&kind=&limit=` | 列出项目内记忆 |
| `GET /v1/memory/recall?project_id=&q=&limit=&kind=` | 按语义 + 词法混合检索记忆（`q` 必填）。响应含 `memories`（带 `score`）、`mode` |
| `DELETE /v1/memory/{id}` | 删除一条自己拥有的记忆 |

记忆对象：`id` / `project_id` / `kind` / `content` / `salience` / `last_accessed_at` / `created_at`。

### 模型列表

两组"模型"端点，用途不同：

**AIGC 创作模型目录（公开，无需认证）**——文生图 / 视频 / 解析模型：

```bash
curl "$BASE/v1/models?type=image"
```

`GET /v1/models`，query：`type`（`image` / `video` / `digital_human` / `hotparse`，缺省 = 全部）。响应：

```json
{
  "models": [
    {
      "code": "…",
      "type": "image",
      "display_name": "…",
      "provider_code": "…",
      "price_credits": 10,
      "pricing_rule": {},
      "config": {},
      "sort_order": 1
    }
  ]
}
```

**对话模型目录（需认证）**——聊天 / 编码可用的大语言模型（含加价后实际计费单价）：

`GET /v1/me/models`（query：`status`，默认只列 `active`）。响应字段：`code` / `display_name` / `family` / `context_window` / `capabilities` / `mode` / `min_plan` / `max_output` / `pricing`（`currency` + `input_per_mtok` + `output_per_mtok`）/ `is_default_chat`（平台默认对话模型标记）。

### AIGC 生成任务

提交异步生成任务（图片 / 视频 / 爆款解析），轮询状态，产物经 CAS 下载：

**提交任务**

```bash
curl -X POST "$BASE/v1/generations" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "image",
    "model_code": "…",
    "prompt": "一只在月球上喝咖啡的猫，水彩风格",
    "params": { "width": 1024, "height": 1024 }
  }'
```

body：

| 字段 | 说明 |
|------|------|
| `type`* | `image` / `video` / `hotparse`（`digital_human` 暂未开放，返回 `501`） |
| `model_code`* | 模型 code（从 `GET /v1/models` 取，须与 `type` 匹配且已启用） |
| `prompt`* | 提示词（`hotparse` 可为空） |
| `negative_prompt` | 可选负向提示词 |
| `params` | 可选模型参数（尺寸 / 时长等） |
| `is_public` | 是否公开到画廊 |
| `parent_sha` / `lineage_op` | 可选，产物血缘（基于上一代产物的再创作） |
| `idempotency_key` | 可选客户端去重键 |

响应 `200`：`task`（含 `id` / `status` / `progress` / `cost_credits`）、`estimated_seconds`、`balance_after`。扣费发生在实际生成阶段，提交本身不扣。

**查询与管理**

| 方法 + 路径 | 说明 |
|------|------|
| `GET /v1/generations/mine?statuses=&type=&limit=&offset=` | 我提交的任务列表（含产物 `outputs`） |
| `GET /v1/generations/{id}` | 任务详情（自己的任务或公开任务） |
| `POST /v1/generations/{id}/cancel` | 取消排队中的任务 |
| `PATCH /v1/generations/{id}/visibility` | 切换公开 / 私有 |
| `DELETE /v1/generations/{id}` | 删除任务 |

任务 `status` 流转：`pending` → `running` → `succeeded` / `failed` / `cancelled`；失败时带 `error_code` / `error_message`。

```bash
# 轮询任务状态
curl -H "Authorization: Bearer $TOKEN" "$BASE/v1/generations/$TASK_ID"
```

---

## 未覆盖的端点

以下对外可达的端点组不属于第三方集成的核心路径，本文不展开（部分见其他章节）：

- `/v1/messages` —— 模型调用网关（对话补全，计费出口）
- `/v1/agents` / `/v1/skills` —— Agent 运行时与技能管理
- `/v1/threads` / `/v1/chat/*` —— 会话与聊天统计
- `/v1/files` / `/v1/brain/*` —— 通用文件上传与 CAS 下载
- `/v1/notes` / `/v1/notebooks` / `/v1/note-tags` / `/v1/shares/*` —— 笔记与分享
- `/v1/realtime/` —— SSE 实时通知
- `/v1/me/usage`、`/v1/credits/*`、`/v1/plans` 等 —— 用量与订阅计费
