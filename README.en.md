<div align="center">

# BiuMind — Your AI Workbench

**Write docs · Run agents · Ship code · Take notes — bring scattered AI tools into one workbench, with data connected.**

[简体中文](./README.md) · **English**

Instant cloud sign-up · One-command self-hosting · One account across desktop / mobile / browser / CLI

[Quick Start](#quick-start) · [Architecture](#architecture-overview) · [Website](https://biumind.ai) · [Contributing](./CONTRIBUTING.md)

</div>

---

## What is BiuMind?

BiuMind is an all-in-one AI work platform: writing docs, running agents, shipping code, and taking notes — work that used to be scattered across multiple tools — now lives in a single workbench with **connected data**. Conversations, web clips, documents, and creative output all settle into one knowledge base, linked by AI into a knowledge graph, reusable across every scenario.

Two deployment tracks:

- **Cloud (sign up and go)**: create an account in seconds, with a free monthly allowance included. Best for individuals and small teams.
- **Self-hosted**: bring up the full stack with one command; your data never leaves your own servers. Best for enterprises and data-sensitive scenarios.

## Core Features

Six modules covering a full day of AI work:

| Module | Capabilities |
|--------|--------------|
| 🧠 **Knowledge Hub** | Block editor + knowledge graph + AI memory; docs, conversations, and web clips settle into one place, retrievable anytime via semantic search |
| 💻 **Coding Workbench** | Multiple AI engineers write code, run tests, and open PRs in parallel; file edits and shell commands ask for your approval first; switch between desktop / mobile / CLI, tasks keep running in the cloud |
| 🎨 **Creation** | Text-to-image, text-to-video, viral-content breakdowns; output flows into the knowledge base for reuse and remixing |
| ☁️ **Cloud Workspace** | Sessions and memory live in the cloud — switch devices without losing context |
| 📮 **Channels** | AI plugs into Feishu / Telegram / Slack / Discord / email, with auto-reply + knowledge base recall |
| 📦 **App Center** | Ready-to-use professional AI assistants: RSS digests, email summaries, stock tracking, paper tracking, and more |

## Cross-Platform

One account, four front ends, switching anytime — **the CLI and GUI share the same kernel and the same sessions**:

- **Desktop (primary workbench)**: a single Flutter codebase covering macOS / Windows / Linux
- **Mobile (companion)**: iOS / Android — approve agent work, check sessions, and look up the knowledge base on the go; plus a uni-app mini-program targeting 9 platforms (WeChat / Alipay / Douyin / Baidu / QQ / Kuaishou / JD / Feishu / H5)
- **Browser**: web clipper extension (Chrome / Edge) — save anything you see into the knowledge base with one click
- **Command line**: the `biu` CLI — chat, run agents, and operate the knowledge base from your terminal

## Architecture Overview

BiuMind is a monorepo: front ends, back ends, and shared components all live in one repository.

- **Backend**: 11 Go microservices; HTTP + JWT for inter-service communication, NATS JetStream for async messaging, Postgres (pgvector + ltree) as the unified data layer — each service owns its schema with goose migrations.
- **Agent protocol**: clients and the CLI talk to the Runtime over a bidirectional WebSocket **SDK Protocol** — JSON wire format + JSON Schema (`schema/sdk/v1/`), implemented three ways: hand-written Go structs / Dart json_serializable / TS Zod. Server-side business APIs are separately defined in buf-managed proto3 (`packages/proto/`).
- **Model gateway**: `model-relay` is the single egress for all model calls, supporting BYOK (bring your own key) and a platform pool, with unified metering.
- **Async jobs**: Python workers pull tasks from NATS (document parsing, AIGC generation, wiki ingestion, risk control).

### Backend Services (`services/`)

| Service | Responsibility |
|---------|----------------|
| `identity` | Accounts & auth: registration / login / multi-platform tokens, credits and BYOK management |
| `authz` | Authorization policy engine (policy + cache) |
| `realtime` | Realtime push: WebSocket hub + NATS fanout |
| `model-relay` | Model gateway: single egress, BYOK + platform pool, unified billing |
| `runtime` | Agent runtime engine (shares its kernel with the `biu` CLI) |
| `brain` | Knowledge layer: Wiki / Graph / Memory / Search |
| `app_center` | App Center: BiuApp registry and runtime (built-in reference apps: RSS / translate / tasks) |
| `aigc` | AIGC access layer: text-to-image / video / digital human (execution delegated to workers) |
| `channels` | Multi-channel distribution: Telegram and other drivers, toggled by environment |
| `sandbox` | Cloud sandboxes: K8s pod-per-sandbox, pluggable drivers |
| `deploy` | One-click deploy: JWT-gated deploy API + static hosting |

> The `billing` and `presence` services are on the roadmap and not yet created (see the comments in `go.work`).

## Repository Layout

```
biumind/
├── apps/                 # Front-end products
│   ├── client/           #   Flutter multi-platform main client (desktop / mobile / web)
│   ├── cli/biu/          #   biu Go CLI (shares the kernel with the server)
│   ├── webclip/          #   Browser clipper extension (Chrome / Edge MV3)
│   └── miniapp/          #   uni-app cross-platform mini-program (9 targets)
├── web/site/             # Official site biumind.ai (Astro + Tailwind, zh/en)
├── admin/                # Instance admin console (Vue 3 + Element Plus)
├── services/             # Backend Go services (see table above, 11 total)
├── workers/              # Python workers: ingest / aigc / wiki-llm / wiki-parse / risk-control
├── packages/             # Shared across products: proto (buf) / go-sdk / skills-stdlib
├── schema/               # SDK Protocol JSON Schema (sdk/v1) + release manifests (release/v1)
├── sdks/                 # Public integration SDKs (go / python / node, Apache-2.0)
├── extensions/vscode/    # VS Code extension
├── deploy/docker-compose/# Local full stack: PG / Redis / MinIO / NATS + services + workers
└── tools/                # Bootstrap / schema validation / architecture invariant scripts
```

## Quick Start

Prerequisites: Go 1.26+, Flutter (stable), buf, uv (Python), Docker.

```bash
task bootstrap            # one-time dependency install (buf / goimports / dart / uv ...)

# Bring up the local full stack (PG / Redis / MinIO / NATS + services)
cd deploy/docker-compose
cp .env.example .env      # ⚠️ replace every *_change_me placeholder with a strong random value
make up-infra && make health   # infra only; use make up / up-all for the full stack

# Run the model gateway + CLI
task model-relay:run
task cli:install          # biu → ~/.local/bin/biu
biu                       # enter the REPL
```

Note: `deploy/docker-compose` is a dev/test local stack example, not a production deployment; the `sandbox` and `deploy` services depend on K8s / S3 and are not part of the local stack.

## Development

```bash
task proto:generate       # buf codegen for Go / Dart / TS
task test                 # full test suite (Go + Dart + Python)
task lint                 # lint:go + lint:dart + lint:proto
task lint:invariants      # architecture invariant checks (enforced in CI)
task --list               # full task list
```

See [CONTRIBUTING.md](./CONTRIBUTING.md) for contribution guidelines.

## Security

Report vulnerabilities privately via [SECURITY.md](./SECURITY.md) — **do not open public issues**.
Before self-hosting, make sure to rotate every placeholder secret in `deploy/docker-compose/.env.example`.

## License

BiuMind uses a **tiered license**:

- **Platform / product** (`apps/`, `admin/`, `services/`, `workers/`, `packages/`, `deploy/`, `schema/`): **[BiuMind Community License](./LICENSE)** — source-available; free for personal, research, educational, and **internal company** use (including internal commercial use), but **offering BiuMind to third parties as a public SaaS / hosted service** and commercially distributing derivatives are prohibited. For commercial licensing, contact `geekidentity@163.com`.
- **SDKs + extensions** (`sdks/`, `extensions/`): **Apache-2.0** — embedding and integration encouraged.

Vendored third-party code is credited in the LICENSE files of their respective directories (miniflux, webclip readability·turndown, rss_icons).
