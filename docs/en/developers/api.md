# API Reference (MCP + REST)

BiuMind offers two integration surfaces:

- **MCP (Model Context Protocol)** — recommended. One endpoint exposes knowledge base reading/writing, memory, search, and Q&A as tools that AI clients can call directly.
- **REST API** — conventional HTTP endpoints grouped by module, suited to scripts, CI, and server-side integrations.

Both share the same authentication (Bearer token).

## Basics

| Item | Value |
|----|----|
| Cloud Base URL | `https://biumind.ai` |
| Self-hosted / local Base URL | `http://localhost:8088` (the compose stack's default entry point) |
| Request / response format | JSON (`Content-Type: application/json`) |
| Authentication | `Authorization: Bearer <token>` (see [Authentication](#authentication)) |

In all examples below, `$BASE` refers to the Base URL and `$TOKEN` to your Bearer token.

Unified error shape (identical across all REST endpoints):

```json
{
  "error": { "code": "not_found", "message": "" }
}
```

Common status codes: `400` invalid parameters, `401` missing / invalid token, `403` forbidden (resource not owned by the current user), `404` not found, `409` version conflict (writes carrying `If-Match`), `429` rate limited.

> [!NOTE]
> Admin endpoints (`/v1/admin/*`) and internal service-to-service endpoints (`/v1/internal/*`) are not publicly available and are not covered in this document. Streaming channels — WebSocket (SDK Protocol), SSE realtime notifications — are covered in [SDK Protocol](./sdk-protocol.md).

---

## Authentication

All business endpoints require a Bearer token. BiuMind supports three ways to obtain one; for third-party integrations the **API Token (PAT)** is recommended.

### Option 1 (recommended): API Token (PAT)

A PAT is a long-lived programmatic access token with the format `bm_<8-char prefix>_<JWT>` and a default validity of 1 year. Put the entire token string into the `Authorization` header as-is; the server recognizes the `bm_` prefix and verifies it as a JWT — it works directly on every business endpoint (REST and MCP).

**Generate it in the client**: open the BiuMind client → Settings → **API Tokens** tab. After creation the token is shown in plaintext **only once** — save it immediately.

**Generate it via the API** (requires logging in with email and password first to obtain a JWT; a PAT cannot mint another PAT):

```bash
# Create a PAT (name is required)
curl -X POST "$BASE/v1/identity/me/tokens" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-integration",
    "scopes": [],
    "ttl_seconds": 0
  }'
```

Request fields:

| Field | Type | Description |
|------|------|------|
| `name` | string | Required. Display name |
| `scopes` | string[] | Optional. Scope labels |
| `workspace_id` / `project_id` | string (UUID) | Optional. Scope restriction |
| `ttl_seconds` | int | Optional. Validity in seconds; `0` or omitted = 1 year (can only be shortened, never extended) |

Response `201`:

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

`secret` is the complete token; it is **returned only once, in the creation response** — the server never stores the plaintext.

Companion management endpoints:

| Method + path | Description |
|------|------|
| `GET /v1/identity/me/tokens` | List my PATs (no secrets; only the `redacted` prefix is shown) |
| `DELETE /v1/identity/me/tokens/{id}` | Revoke a PAT; responds `202` |
| `GET /v1/identity/whoami` | Validate any token: returns `user_id` / `roles` / `plan` / `scope` / `expires_at`, etc. |

> [!WARNING]
> A PAT carries the full permissions of the account — guard it like a password. If it leaks, revoke it immediately in the client or via `DELETE /v1/identity/me/tokens/{id}`. A PAT cannot be used to create another PAT (preventing privilege chains from spreading).

### Option 2: email / password login for a JWT

For scenarios where you control the full login flow yourself. The access token is short-lived (`expires_in_seconds` in the response); exchange the refresh token for a new one when it expires.

```bash
curl -X POST "$BASE/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{ "email": "you@example.com", "password": "…", "device_name": "my-script" }'
```

Response `200`:

```json
{
  "access_token": "eyJhbGciOi…",
  "refresh_token": "…",
  "expires_in_seconds": 86400,
  "user": { "id": "…", "email": "…", "display_name": "…", "email_verified": true }
}
```

Refresh (rotating: returns a new access token plus a new refresh token; the old refresh token is invalidated at the same time):

```bash
curl -X POST "$BASE/v1/auth/refresh" \
  -H "Content-Type: application/json" \
  -d '{ "refresh_token": "…" }'
```

> [!NOTE]
> Logging in with an account that has not completed email verification returns `403 email_not_verified`; no token is issued.

### Option 3: OAuth 2.1

BiuMind is also an OAuth 2.1 authorization server (primarily for CLI browser login; usable by third-party apps as well). Endpoints:

| Path | Description |
|------|------|
| `GET /.well-known/oauth-authorization-server` | RFC 8414 server metadata (the discovery entry point) |
| `GET /oauth/authorize` | Authorization endpoint |
| `POST /oauth/token` | Token endpoint |
| `POST /oauth/revoke` | Token revocation |
| `POST /oauth/register` | Dynamic client registration |

Start discovery from the metadata endpoint; this document does not go further.

---

## MCP access (recommended)

The MCP server packages the knowledge base (Wiki), memory (Memory), and search capabilities into a set of tools that any MCP-capable AI client or agent can call directly.

### Endpoint and protocol

| Item | Value |
|----|----|
| Endpoint | `POST /v1/mcp` (fixed path; no sub-paths, no sessions) |
| Authentication | `Authorization: Bearer <JWT or PAT>`, verified on every request |
| Protocol | JSON-RPC 2.0 over HTTP; MCP protocol version `2025-03-26` |
| Server info | `serverInfo`: `biumind-brain` / `0.2.0` |
| Max request size | 1 MB |

Transport shape: each `POST /v1/mcp` carries one JSON-RPC request and returns one JSON response. **There is no SSE streaming and no long-lived session** — for streaming or local-process form factors, see [stdio transport](#stdio-transport-self-hosted) below.

Supported methods:

| Method | Description |
|------|------|
| `initialize` | Handshake; returns the protocol version, `serverInfo`, and `capabilities` |
| `tools/list` | Returns all tool definitions (including JSON Schema parameters) |
| `tools/call` | Call a tool (`params.name` + `params.arguments`) |
| `ping` | Liveness probe; returns `{}` |

JSON-RPC error codes follow the protocol's reserved values: `-32700` parse error, `-32600` invalid request (including authentication failure), `-32601` method / tool not found, `-32602` invalid params, `-32603` internal error.

Tool results uniformly use the standard MCP envelope: `content` (an array of human-readable text) + `structuredContent` (machine-readable structured data; the per-tool return fields are listed below) + `isError`.

### Handshake example

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

### Tools overview

All tools isolate data by the token's user (only projects owned by that user are accessible). `project_id` is a UUID everywhere; discover one with `wiki.list_projects`.

#### Memory

| Tool | Parameters | Returns (structuredContent) |
|------|------|------|
| `memory.store` | `project_id`*, `content`*, `kind` (`recall`/`preference`/`habit`, default `recall`), `salience` (0–1, default 0.5) | `memory` (includes `id`) |
| `memory.list` | `project_id`*, `kind`, `limit` (1–500, default 100) | `memories` |
| `memory.recall` | `project_id`*, `query`*, `kind`, `limit` (1–50, default 10) | `memories` (each with `score`), `mode` (`lexical` or `hybrid`), `query` |
| `memory.delete` | `id`* (memory UUID) | `deleted` |

#### Wiki

| Tool | Parameters | Returns (structuredContent) |
|------|------|------|
| `wiki.list_projects` | `limit` (1–500, default 100) | `projects` (`id` / `name` / `created_at`). **Call this first to get a project_id** |
| `wiki.search` | `query`*, `project_id` (omitted = across all of the current user's projects), `limit` (1–100, default 20) | `hits` (`kind` / `page_id` / `project_id` / `title` / `snippet` / `score`), `mode` (`bm25` or `hybrid`) |
| `wiki.list_pages` | `project_id`*, `limit` (1–500, default 100) | `pages` |
| `wiki.get_page` | `page_id`*, `include_blocks` (default true) | `page` (with `body_md` / `frontmatter` / `version`; blocks capped at 200) |
| `wiki.create_page` | `project_id`*, `title`*, `parent_id`, `frontmatter` (object) | `page` |
| `wiki.update_page` | `page_id`*, `title`, `frontmatter`, `version` (optimistic lock; pass the `version` obtained from `get_page`; omitted = force overwrite) | `page`; on version conflict, fails with `current_version` attached |
| `wiki.ingest` | `project_id`*, `raw_text`* (Markdown / plain text), `title` | `task` (async ingestion task; the LLM splits it into pages and builds the library automatically) |
| `wiki.list_reviews` | `project_id`*, `kind` (`dedup`/`lint`/`sweep`/`merge`/`suggestion`/`contradiction`), `status` (default `open`), `limit` | `reviews`, `status` |
| `wiki.dismiss_review` | `id`* (review UUID) | `id`, `status` |
| `wiki.merge_pages` | `canonical_id`* (the page kept), `duplicate_id`* (the page merged into it, soft-deleted) | `canonical_id`, `duplicate_id`, `merged` |
| `wiki.related_pages` | `page_id`*, `limit` (1–100, default 20) | `related` (`page_id` / `title` / `score` / `signals`) |
| `wiki.chat` | `project_id`*, `message`*, `mode` (`fast`/`standard`/`deep`, default `standard`), `model` (omitted = the platform's default chat model) | `answer`, `cited_pages`, `model`, `mode`, `prompt_tokens`, `completion_tokens` |

`*` = required.

> [!NOTE]
> `wiki.chat` runs a read-only LLM Q&A loop on the server (it retrieves pages within the project, answers, and cites sources) and is billed as a normal model call. `fast`/`standard`/`deep` correspond to increasing retrieval and iteration budgets.

> [!WARNING]
> Some tools depend on deployment-side optional components (vector search, ingestion queue, Q&A model, etc.). When these are not configured, `tools/list` still lists the tool, but calling it returns an internal error frame — this is by design, keeping the tool list stable across deployments.

### Call example

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
      "arguments": { "query": "agent architecture", "limit": 5 }
    }
  }'
```

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [ { "type": "text", "text": "5 hits for \"agent architecture\" (mode=hybrid)" } ],
    "structuredContent": {
      "hits": [
        {
          "id": "wiki:page:…",
          "kind": "page",
          "page_id": "…",
          "project_id": "…",
          "title": "Agent architecture",
          "snippet": "…",
          "score": 0.019,
          "updated_at": "2026-09-01T00:00:00Z"
        }
      ],
      "mode": "hybrid",
      "query": "agent architecture"
    },
    "isError": false
  }
}
```

### stdio transport (self-hosted)

The repository ships a standalone stdio MCP server (JSON-RPC 2.0 over standard input/output, the same set of tools) that local AI clients can launch as a subprocess. Build from source:

```bash
go build -o biu-memory-mcp ./services/brain/cmd/memory-mcp
```

Minimal environment variables:

```json
{
  "mcpServers": {
    "biumind-memory": {
      "command": "/usr/local/bin/biu-memory-mcp",
      "env": {
        "DATABASE_URL": "postgres://…",
        "MEMORY_MCP_USER_ID": "<your user UUID>",
        "MEMORY_MCP_PROJECT_ID": "<project UUID>"
      }
    }
  }
}
```

Optional configuration: `EMBED_PROVIDER` / `EMBED_BASE_URL` / `EMBED_API_KEY` / `EMBED_MODEL` / `EMBED_DIMS` (enables semantic search), plus the NATS address (enables `wiki.ingest`).

> [!WARNING]
> The stdio transport pins the user identity via environment variables and **performs no JWT verification** — any local process able to start it can operate as that user. It is only suitable for single-user machines; for multi-tenant / cloud deployments use the HTTP transport (`POST /v1/mcp`).

---

## REST API

All endpoints below require `Authorization: Bearer <JWT or PAT>`, except where explicitly marked "public". All write endpoints isolate by resource ownership: accessing a project you do not own returns `404`/`403`.

Versioned write operations (updating pages / blocks) support optimistic concurrency: `GET` responses carry an `ETag` (the version number); include the `If-Match: <version>` request header on writes; a version mismatch returns `409` with `server_version` / `server_payload` attached.

### Wiki

#### Projects

**Create a project**

```bash
curl -X POST "$BASE/v1/wiki/projects" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "name": "My knowledge base" }'
```

`POST /v1/wiki/projects`, body: `name`* (project name), `template_id` (optional template). Response `200`: the project object (`id` / `name` / `created_at` / `template_id`).

**List projects**

`GET /v1/wiki/projects` → `{"projects": [...]}` (up to 100).

#### Pages

| Method + path | Description |
|------|------|
| `POST /v1/wiki/projects/{pid}/pages` | Create a page. Body: `title`*, `parent_id` (optional parent page; forms a tree) |
| `GET /v1/wiki/projects/{pid}/pages` | List pages in the project (up to 200) |
| `GET /v1/wiki/projects/{pid}/pages/{id}` | Page detail; the response carries an `ETag` |
| `PUT /v1/wiki/projects/{pid}/pages/{id}` | Update the title / frontmatter. Body: `title`, `frontmatter` (object); may carry `If-Match` |
| `PUT /v1/wiki/projects/{pid}/pages/{id}/body` | Write the full Markdown body. Body: `{"body_md": "…"}`; may carry `If-Match`. The server automatically recomputes the content-block projection |
| `DELETE /v1/wiki/projects/{pid}/pages/{id}` | Soft-delete a page |

Page object:

```json
{
  "id": "…",
  "project_id": "…",
  "title": "Page title",
  "frontmatter": {},
  "body_md": "# Markdown body",
  "share_mode": "private",
  "version": 3,
  "created_at": "2026-09-01T00:00:00Z",
  "updated_at": "2026-09-10T00:00:00Z"
}
```

A complete example of writing the body:

```bash
curl -X PUT "$BASE/v1/wiki/projects/$PID/pages/$PAGE_ID/body" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "If-Match: 3" \
  -d '{ "body_md": "# Updated body\n\nNew content." }'
```

#### Content blocks

The structured projection of a page body, readable and writable on its own:

| Method + path | Description |
|------|------|
| `GET /v1/wiki/projects/{pid}/pages/{id}/blocks` | List all blocks of a page |
| `POST /v1/wiki/projects/{pid}/pages/{id}/blocks` | Create a block. Body: `position` (numeric sort position), `type` (default `text`), `content` (object) |
| `PUT /v1/wiki/projects/{pid}/blocks/{id}` | Update a block. Body: `content`, `position`; may carry `If-Match` |
| `DELETE /v1/wiki/projects/{pid}/blocks/{id}` | Soft-delete a block |

#### Incremental changes

| Method + path | Description |
|------|------|
| `GET /v1/wiki/projects/{pid}/changes?since={id}&limit=200` | Pull the project's event stream after `since` (`page.updated` / `block.deleted`, etc.) for polling-based sync. `limit` caps at 1000 |

### Search

**Unified search** (multi-source fused retrieval across knowledge base / web / notes):

```bash
curl -X POST "$BASE/v1/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{ "query": "vector search", "scope": "wiki", "limit": 20 }'
```

`POST /v1/search`, body:

| Field | Description |
|------|------|
| `query`* | Search terms |
| `scope` | `wiki` (default; pages + blocks) / `web` (external web) / `all` (fused) |
| `project_id` | Optional; restricts to a single project |
| `limit` | 1–100, default 20 |
| `include_notes` | When `true`, personal notes are included in the search (default `false`) |

The response is grouped by source (`wiki` / `web` / `vector` / `graph` / `notes` / `images`) and additionally carries the fused ranking in `fused`; `wiki` hits include `page_id` / `title` / `snippet` / `score`.

### Graph

| Method + path | Description |
|------|------|
| `GET /v1/graph/projects/{pid}/nodes?q=&kind=&limit=` | List / search graph nodes (entities) |
| `GET /v1/graph/projects/{pid}/nodes/{id}` | Node detail + one-hop edges (`edges`) + back-references (`backlinks`) |
| `GET /v1/graph/projects/{pid}/related?node_id=&depth=2&relations=&limit=` | BFS-expand neighbors from a seed node; `depth` defaults to 2, `relations` is a comma-separated filter |
| `POST /v1/graph/projects/{pid}/extract` | Manually trigger entity extraction on a content block. Body: `block_id`, `content` (object); returns the extracted and upserted nodes |

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/graph/projects/$PID/related?node_id=$NODE_ID&depth=2"
```

Response: `{"neighbors": [{…node fields, "depth": 1, "relation": "…"}]}`.

### Memory

Four endpoints; the target `project_id` must belong to the current user:

```bash
# Store a memory
curl -X POST "$BASE/v1/memory" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "project_id": "$PID",
    "kind": "preference",
    "content": "The user prefers concise replies in Chinese",
    "salience": 0.8
  }'
```

| Method + path | Description |
|------|------|
| `POST /v1/memory` | Body: `project_id`*, `content`*, `kind` (`recall`/`preference`/`habit`), `salience` (0–1). Response: the memory object |
| `GET /v1/memory?project_id=&kind=&limit=` | List memories in a project |
| `GET /v1/memory/recall?project_id=&q=&limit=&kind=` | Hybrid semantic + lexical memory search (`q` required). The response contains `memories` (with `score`), `mode` |
| `DELETE /v1/memory/{id}` | Delete a memory you own |

Memory object: `id` / `project_id` / `kind` / `content` / `salience` / `last_accessed_at` / `created_at`.

### Model catalog

Two groups of "model" endpoints with different purposes:

**AIGC creation model catalog (public, no authentication)** — text-to-image / video / teardown models:

```bash
curl "$BASE/v1/models?type=image"
```

`GET /v1/models`, query: `type` (`image` / `video` / `digital_human` / `hotparse`; omitted = all). Response:

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

**Chat model catalog (authentication required)** — the LLMs available for chat / coding (with the actual unit prices after markup):

`GET /v1/me/models` (query: `status`; by default only `active` ones are listed). Response fields: `code` / `display_name` / `family` / `context_window` / `capabilities` / `mode` / `min_plan` / `max_output` / `pricing` (`currency` + `input_per_mtok` + `output_per_mtok`) / `is_default_chat` (marks the platform's default chat model).

### AIGC generation tasks

Submit an async generation task (image / video / viral-content teardown), poll its status, and download artifacts via CAS:

**Submit a task**

```bash
curl -X POST "$BASE/v1/generations" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "image",
    "model_code": "…",
    "prompt": "A cat drinking coffee on the moon, watercolor style",
    "params": { "width": 1024, "height": 1024 }
  }'
```

Body:

| Field | Description |
|------|------|
| `type`* | `image` / `video` / `hotparse` (`digital_human` is not yet available; returns `501`) |
| `model_code`* | Model code (from `GET /v1/models`; must match the `type` and be enabled) |
| `prompt`* | Prompt (may be empty for `hotparse`) |
| `negative_prompt` | Optional negative prompt |
| `params` | Optional model parameters (size / duration, etc.) |
| `is_public` | Whether to publish to the gallery |
| `parent_sha` / `lineage_op` | Optional; artifact lineage (re-creation based on a previous-generation artifact) |
| `idempotency_key` | Optional client-side deduplication key |

Response `200`: `task` (with `id` / `status` / `progress` / `cost_credits`), `estimated_seconds`, `balance_after`. Charging happens at the actual generation stage; submission itself is free.

**Query and manage**

| Method + path | Description |
|------|------|
| `GET /v1/generations/mine?statuses=&type=&limit=&offset=` | Tasks I submitted (including artifacts in `outputs`) |
| `GET /v1/generations/{id}` | Task detail (your own tasks or public tasks) |
| `POST /v1/generations/{id}/cancel` | Cancel a queued task |
| `PATCH /v1/generations/{id}/visibility` | Toggle public / private |
| `DELETE /v1/generations/{id}` | Delete a task |

Task `status` transitions: `pending` → `running` → `succeeded` / `failed` / `cancelled`; failures carry `error_code` / `error_message`.

```bash
# Poll task status
curl -H "Authorization: Bearer $TOKEN" "$BASE/v1/generations/$TASK_ID"
```

---

## Endpoints not covered

The following externally reachable endpoint groups are not on the core path for third-party integrations and are not detailed here (some are covered in other chapters):

- `/v1/messages` — the model-call gateway (chat completions; the billing egress)
- `/v1/agents` / `/v1/skills` — agent runtime and skill management
- `/v1/threads` / `/v1/chat/*` — conversations and chat statistics
- `/v1/files` / `/v1/brain/*` — generic file upload and CAS download
- `/v1/notes` / `/v1/notebooks` / `/v1/note-tags` / `/v1/shares/*` — notes and sharing
- `/v1/realtime/` — SSE realtime notifications
- `/v1/me/usage`, `/v1/credits/*`, `/v1/plans`, etc. — usage and subscription billing
