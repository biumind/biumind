# Environment Variables Reference

All configuration for the compose stack is injected via `deploy/docker-compose/.env`: `docker compose` reads the `.env` in the same directory automatically, and the Makefile also does `include .env` and `export`s it (so standalone commands like `make psql` work the same way). On first use, run `cp .env.example .env`.

This document is a **complete transcription** of every variable, grouped by the section comments in `.env.example`, with each entry cross-checked against `docker-compose.yml`. Column meanings:

- **Required**: `Required` = no default in compose (or enforced by `make check-env`); the stack cannot start without it. `Replace in production` = compose has a dev default, but it must be replaced for production / self-hosting. `Optional` = things run fine without it.
- **Default**: the fallback value of `${VAR:-default}` in the compose file; `—` means no fallback — the value is taken directly from `.env`.
- **Referencing services**: the services in the compose file that actually reference the variable (including all services covered by the common anchors `x-svc-env` / `x-worker-env` after expansion).

> [!NOTE]
> When a variable exists both in `.env` and as a compose default, `.env` wins. "All Go services" below refers to the services covered by the `x-svc-env` anchor: authz / realtime / identity / model-relay / brain / runtime / app-center / channels / aigc.

---

## General

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `COMPOSE_PROJECT_NAME` | Optional | `biumind` | compose project name (determines the container / volume prefix). The compose file already pins `name: biumind` at the top; usually no need to set | None (built-in compose CLI variable) |
| `BIUMIND_ENV` | Optional | `test` | Runtime environment identifier, injected into all services and workers | All Go services (x-svc-env), all workers |
| `BIUMIND_LOG_LEVEL` | Optional | `info` (example gives `debug`) | Log level | All Go services, all workers |
| `TZ` | Optional | `UTC` (example gives `Asia/Shanghai`) | Container timezone | All Go services, postgres |

## Image Registry

Business images = `${BIUMIND_REGISTRY}/biumind/<name>:${BIUMIND_TAG}` — the namespace is fixed at `biumind`, only the host changes.

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `BIUMIND_REGISTRY` | Optional | `registry.cn-beijing.aliyuncs.com` | Image registry host for business images. Choices: `ghcr.io` (GitHub GHCR), `docker.io` (Docker Hub) | The `image:` field of all business images (9 Go services + site / web-client / admin-web / miniapp-h5 + 3 workers) |
| `BIUMIND_TAG` | Optional | `main` | Image tag. `main` updates on every push to main; you can also pin a `v*` release version or a `sha-<short-hash>` | Same as above. Local builds go through `make build-images` + `make up-local` (tag `dev`); no need to set this here |

## Ports

Change these only when the ports conflict on your machine. All of them are port mappings of the form `${VAR:-default}:container-port`.

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `POSTGRES_PORT` | Optional | `5432` | Postgres external port (also interpolated into every service's DATABASE_URL) | postgres; the DATABASE_URL of all database-connected services |
| `MINIO_API_PORT` | Optional | `9000` | MinIO API port | minio |
| `MINIO_CONSOLE_PORT` | Optional | `9001` | MinIO console port (`make minio-console` also reads it) | minio |
| `NATS_PORT` | Optional | `4222` | NATS client port | nats |
| `NATS_MONITOR_PORT` | Optional | `8222` | NATS monitor port | nats |
| `MODEL_RELAY_PORT` | Optional | `7001` | model-relay port | model-relay |
| `RUNTIME_PORT` | Optional | `7002` | runtime port | runtime |
| `BRAIN_PORT` | Optional | `7003` | brain port | brain |
| `IDENTITY_PORT` | Optional | `7004` | identity port | identity |
| `CHANNELS_PORT` | Optional | `7007` | channels port | channels |
| `REALTIME_PORT` | Optional | `7008` | realtime port | realtime |
| `AUTHZ_PORT` | Optional | `7009` | authz port (`make authz-eval` also reads it) | authz |
| `AIGC_PORT` | Optional | `7012` | aigc port | aigc |

> [!NOTE]
> There are two more port-mapping variables not listed in `.env.example`: `SITE_PORT` (default `8088`, the unified site entry) and `APP_CENTER_PORT` (default `7011`). To change these two ports, set them in `.env` the same way — see [Variables referenced by compose but not listed in .env.example](#variables-referenced-by-compose-but-not-listed-in-envexample) below.

## Postgres

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `POSTGRES_USER` | Optional | `biumind` | Database user | postgres; the DATABASE_URL of all services; `make psql` / `backup-pg` / `restore-pg` |
| `POSTGRES_PASSWORD` | Replace in production | `biumind_dev_password_change_me` | Database password | Same as above |
| `POSTGRES_DB` | Optional | example gives `biumind` (compose / Makefile fall back to `biu_core`) | Database name | Same as above |
| `POSTGRES_HOST` | Optional | `postgres` (commented out) | To use an external Postgres (RDS / self-managed), uncomment and fill in the address | DATABASE_URL of all services |
| `POSTGRES_SSLMODE` | Optional | `disable` (commented out) | Externally managed databases often need `require` / `verify-full` | DATABASE_URL of all services |

> [!NOTE]
> The example value of `POSTGRES_DB` differs from the compose / Makefile fallback (`biumind` vs `biu_core`). When `.env` exists, `.env` wins and nothing breaks; only if you delete that line does it fall back to `biu_core` — do not mix the two.

## MinIO / Object Storage

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `MINIO_ROOT_USER` | Optional | `biumind` | MinIO root user; also the source for each service's S3 access key | minio, minio-bootstrap; all Go services and workers (`S3_ACCESS_KEY` / `MINIO_ACCESS_KEY` / `AIGC_S3_ACCESS_KEY` are all derived from it) |
| `MINIO_ROOT_PASSWORD` | Replace in production | `biumind_minio_dev` | MinIO root password; likewise the source for each service's S3 secret key | Same as above |
| `MINIO_BUCKET` | Optional | `biumind` | Main bucket name (created automatically by minio-bootstrap, along with derived buckets such as `<bucket>-snapshots` / `<bucket>-deploy`) | minio-bootstrap; all Go services and workers (`S3_BUCKET`) |

The `.env.example` comments also mention, when switching to external S3 / OSS, setting `S3_ENDPOINT` / `S3_ACCESS_KEY` / `S3_SECRET_KEY` / `S3_BUCKET`, `MINIO_ENDPOINT` / `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY` / `MINIO_USE_SSL`, `AIGC_S3_ENDPOINT` / `AIGC_S3_REGION` / `AIGC_S3_USE_SSL` — note that **only `S3_ENDPOINT`, `MINIO_ENDPOINT`, `MINIO_USE_SSL`, and `AIGC_S3_*` are variable names read directly by compose**; for credentials (`S3_ACCESS_KEY` etc.), compose always derives them from `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD`, so setting them directly has no effect. Connecting external object storage requires editing the compose file — see [Inconsistencies found during cross-checking](#inconsistencies-found-during-cross-checking) below.

## Encryption and Signing

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `BIUMIND_MASTER_KEY` | **Required** | example gives a dev value | Service-level master encryption key, 32 bytes base64-encoded (`openssl rand -base64 32`). Enforced by `make check-env`, which runs before every `make up*`; startup is refused outright if it is missing | model-relay |
| `JWT_SECRET` | **Required** | example gives a dev value | JWT HS256 signing key (no fallback in compose) | All Go services (x-svc-env) |
| `JWT_ISSUER` | Optional | `https://identity.biumind.local` | JWT issuer claim | All Go services |
| `JWT_AUDIENCE` | Optional | `biumind-api` | JWT audience claim | All Go services |
| `BRAIN_SHARE_SIGNING_KEY` | Recommended | empty | HS256 signing key for note-share access JWTs (base64, 32 bytes). Empty = brain generates one randomly at startup (fine for a single instance, but all previously issued guest tokens are invalidated on restart); **multi-instance deployments must explicitly configure the same value** | brain |

## Session Lifetime

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `IDENTITY_ACCESS_TTL` | Optional | `24h` | Access JWT lifetime (example comment: 24h for dev to avoid frequent refreshes; 1h recommended for production) | identity |
| `IDENTITY_REFRESH_TTL` | Optional | `2160h` (90 days) | Refresh token sliding lifetime (each refresh extends it to now + sliding) | identity |
| `IDENTITY_REFRESH_ABSOLUTE_TTL` | Optional | `8760h` (1 year) | Refresh token absolute lifetime (fixed at first issuance; rotation does not reset it; prevents permanent leakage) | identity |
| `IDENTITY_REFRESH_REUSE_GRACE` | Optional | `10s` | Refresh rotation grace window: within the window after a rotation, replaying the old token can still recover along the chain; past the window it is treated as reuse and revokes the whole family | identity |

## Platform LLM Pool

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `BIUMIND_ANTHROPIC_KEY` | Optional | empty | Fallback key for the runtime Agent Plane to reach Anthropic directly (compose maps it to `AGENT_PLANE_ANTHROPIC_API_KEY`). Not the channel for regular model calls; the primary path is credentials stored via "Model Configuration" in the admin console | runtime |

## Search Enhancements (Optional)

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `SEARXNG_URL` | Optional | empty (commented out) | Address of a self-hosted SearxNG instance; when set, brain enables the websearch tool; without it everything still runs, the catalog just has no websearch | brain |
| `RERANK_PROVIDER` | Optional | empty (commented out) | Search rerank provider. Prerequisite: register a model with `mode='rerank'` in the model catalog via the admin console and configure a pricing row, otherwise traffic silently bills zero; unset keeps the original RRF ordering | brain |
| `RERANK_MODEL` | Optional | empty (commented out) | Rerank model name (e.g. `BAAI/bge-reranker-v2-m3`); leave empty for automatic selection via model-relay | brain |

## Wiki OCR (MinerU Self-Hosted, Optional)

Enabling steps: (1) build the MinerU image yourself (no official pre-built one) (2) `docker compose --profile ocr up -d mineru` (3) `WIKI_PARSE_OCR_ENABLED=true`. Once enabled, all PDFs go through MinerU in full, falling back to the plain-text layer on failure.

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `WIKI_PARSE_OCR_ENABLED` | Optional | `false` (commented out) | Master OCR switch (compose maps it to the worker's `BIUMIND_WIKI_PARSE_OCR_ENABLED`) | worker-wiki-parse |
| `MINERU_API_BASE` | Optional | `http://mineru:8000` (commented out) | MinerU service address (compose maps it to `BIUMIND_MINERU_API_BASE`) | worker-wiki-parse |
| `MINERU_IMAGE` | Optional | `mineru:latest` (commented out) | MinerU image name (tag your self-built image, or point to a full image reference) | mineru |
| `OCR_BILLING_MODEL` | Optional | empty (commented out) | Pseudo-model name for OCR billing (billed per page; register the model + pricing row in the admin console); empty = OCR is free | brain |

## Channels (Optional)

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `TELEGRAM_BOT_TOKEN` | Optional | empty | Telegram bot token | channels |
| `DISCORD_BOT_TOKEN` | Optional | empty | Discord bot token | channels |
| `SLACK_BOT_TOKEN` | Optional | empty | Slack bot token | channels |
| `FEISHU_APP_ID` | Optional | empty | Feishu (Lark) App ID | channels |
| `FEISHU_APP_SECRET` | Optional | empty | Feishu (Lark) App Secret | channels |
| `BIUMIND_ANTHROPIC_BASE_URL` | Optional | empty | Anthropic-compatible endpoint override for the runtime Agent Plane (compose maps it to `AGENT_PLANE_ANTHROPIC_ENDPOINT`) | runtime |

## AIGC (Text-to-Image / Video / Digital Humans)

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `AIGC_PORT` | Optional | `7012` | aigc service port | aigc |
| `DASHSCOPE_API_KEY` | Optional | empty | Alibaba Cloud Bailian (DashScope) upstream key; without it the worker rejects the corresponding jobs | aigc, worker-aigc |
| `VOLCENGINE_ARK_API_KEY` | Optional | empty | Volcengine Ark (Doubao Seedream / Seedance) upstream key | aigc, worker-aigc |

## Billing and BYOK

| Variable | Required | Default / example | Purpose | Referencing services |
|----------|----------|-------------------|---------|----------------------|
| `IDENTITY_INTERNAL_TOKEN` | Replace in production | `biumind-dev-internal-token-change-me` | Shared bearer for internal service-to-service calls (`/v1/internal/*`). identity / model-relay / brain / runtime / app-center / aigc / workers must all use the same value | identity, model-relay, brain, runtime, app-center, aigc, worker-aigc, worker-wiki-llm |
| `BYOK_MASTER_KEY` | Recommended | empty | Master encryption key for BYOK (bring your own model key), base64, 32 bytes. **Empty = BYOK is disabled**; never commit it to git; after rotation, old ciphertexts can no longer be decrypted | identity |

---

## Variables Referenced by Compose but Not Listed in .env.example

The following variables are referenced in `docker-compose.yml` but are not listed in `.env.example` (the defaults are almost always fine; add them to `.env` when you need to override).

### Images and Entry Points

| Variable | Default | Purpose | Referencing services |
|----------|---------|---------|----------------------|
| `INFRA_REGISTRY` | `docker.io` | Image registry host for the infrastructure images (pgvector / minio / nats) | postgres, minio, minio-bootstrap, nats |
| `SITE_PORT` | `8088` | External port of the unified site entry | site |
| `RELEASES_UPSTREAM` | `http://minio:9000/releases` | Reverse-proxy upstream for the client `releases.json` (local MinIO / cloud OSS) | site |

### Internal Call Tokens

| Variable | Default | Purpose | Referencing services |
|----------|---------|---------|----------------------|
| `MODEL_RELAY_INTERNAL_TOKEN` | `biumind-dev-relay-internal-token-change-me` | Shared secret for the model-relay internal lane (embedding / rerank); brain and model-relay must use the same value | model-relay, brain |
| `BIUMIND_INTERNAL_TOKEN` | `biumind-dev-internal-token-change-me` | Shared secret for the brain internal Wiki endpoints (`/v1/internal/wiki/*`); brain and the parse workers must use the same value | brain, worker-wiki-parse, worker-wiki-llm |
| `IDENTITY_JWKS_URL` | `http://identity:7004/.well-known/jwks.json` | JWKS public key fetch URL (RS256 signature verification) | All Go services (x-svc-env) |

### Admin Console and Super Administrators

| Variable | Default | Purpose | Referencing services |
|----------|---------|---------|----------------------|
| `BIUMIND_BOOTSTRAP_SUPERADMINS` | empty | Comma-separated email list; identity promotes them to super administrators automatically at startup (emails must already be registered and verified; idempotent; empty = no promotion) | identity |
| `MODEL_RELAY_SYNC_UPSTREAM_URL` | empty | Upstream model catalog address for the admin console "Model Sync" feature | model-relay |
| `MODEL_RELAY_FX_SYNC_URL` | empty | Data source address for the daily exchange-rate sync (USD↔CNY) | model-relay |
| `MODEL_RELAY_FX_SYNC_DISABLED` | `false` | Set to `1` to disable the exchange-rate sync (offline environments) | model-relay |

### Model Preferences

| Variable | Default | Purpose | Referencing services |
|----------|---------|---------|----------------------|
| `EMBED_PROVIDER` | `openai` | Embedding provider | brain |
| `EMBED_MODEL` | empty (automatic selection) | Embedding model; empty = automatically selected via model-relay at startup | brain |
| `EMBED_DIMS` | `1024` | Embedding vector dimensions | brain |
| `PARSE_BILLING_MODEL` | empty | Pseudo-model for cloud document-parsing billing (empty = free) | brain |
| `RUNTIME_DEFAULT_CHAT_MODEL` | empty (resolved by chain) | Fallback default model for task mode (relay default → this → relay preferred → error) | runtime |
| `RADAR_LLM_MODEL` | empty (automatic selection) | App Center radar advisor model (empty = relay selection; still empty = no advisor) | app-center |
| `GITHUB_TOKEN` | empty | GitHub read-only token for App Center Repo Apps; empty = anonymous rate limit of 60 req/h, with a WARN-level degradation at startup | app-center |
| `WIKI_LLM_HUB_URL` | `http://model-relay:7001` | Model gateway address used by the wiki-llm worker (profile `llm`) | worker-wiki-llm |
| `WIKI_LLM_MODEL` | empty (resolved by chain) | Explicit override for the wiki ingestion generation model (owner preference → relay default → relay preferred; do not set a default value or the chain stops working) | worker-wiki-llm |
| `WIKI_LLM_IDENTITY_URL` | `http://identity:7004` | identity address where the wiki-llm worker fetches the user's ingest model preference | worker-wiki-llm |

### Runtime / Channels Operational Parameters

| Variable | Default | Purpose | Referencing services |
|----------|---------|---------|----------------------|
| `RUNTIME_CHANNELS_DEFAULT_USER_ID` | empty | Default user that channel messages are attributed to (fallback when the agent environment is empty) | runtime |
| `RUNTIME_CHANNELS_DEFAULT_PROJECT_ID` | empty | Default project that channel messages are attributed to | runtime |
| `AGENT_PLANE_ADMIN_USER_ID` | empty | Administrator user of the Agent Plane self-registered environment; if unset, the agent environment list is empty | runtime |
| `MINERU_MODEL_SOURCE` | `local` | MinerU model source | mineru |

### Mini-Program / OAuth Credentials (identity)

The following variables all default to empty, are configured on demand, and are referenced only by **identity**:

| Variable | Purpose |
|----------|---------|
| `WECHAT_MP_APPID` / `WECHAT_MP_APPSECRET` | WeChat mini-program |
| `WECHAT_WEB_APPID` / `WECHAT_WEB_APPSECRET` | WeChat Web (H5 OAuth) |
| `H5_FRONTEND_BASE_URL` | H5 OAuth redirect frontend address |
| `ALIPAY_MP_APPID` / `ALIPAY_MP_APPSECRET` / `ALIPAY_MP_PRIVATE_KEY` / `ALIPAY_MP_PUBLIC_KEY` | Alipay mini-program |
| `TOUTIAO_MP_APPID` / `TOUTIAO_MP_APPSECRET` | Douyin mini-program |
| `BAIDU_MP_APPID` / `BAIDU_MP_APPSECRET` | Baidu smart mini-program |
| `QQ_MP_APPID` / `QQ_MP_APPSECRET` | QQ mini-program |
| `KUAISHOU_MP_APPID` / `KUAISHOU_MP_APPSECRET` | Kuaishou mini-program |
| `JD_MP_APPID` / `JD_MP_APPSECRET` | JD mini-program |
| `LARK_MP_APPID` / `LARK_MP_APPSECRET` | Feishu (Lark) mini-program |

### Others

| Variable | Default | Purpose | Referencing services |
|----------|---------|---------|----------------------|
| `IDENTITY_URL` | `http://identity:7004` | identity address where brain fetches user BYOK keys | brain |
| `MINIO_ENDPOINT` | `minio:9000` | MinIO address for the brain file channel | brain |
| `MINIO_USE_SSL` | `false` | Whether the brain file channel uses SSL | brain |
| `AIGC_S3_ENDPOINT` | `http://minio:9000` | AIGC object storage address | aigc, worker-aigc |
| `AIGC_S3_REGION` | `us-east-1` | AIGC object storage region | aigc, worker-aigc |
| `AIGC_S3_USE_SSL` | `false` | Whether AIGC object storage uses SSL | aigc, worker-aigc |
| `AIGC_GENERATE_VIA_RELAY` | `true` | Whether AIGC generation goes through the model-relay egress uniformly | worker-aigc |

---

## Inconsistencies Found During Cross-Checking

While transcribing, each entry was compared against `docker-compose.yml`; the following differences were found — take note when using:

1. **`POSTGRES_DB` has two defaults**: `.env.example` gives `biumind`, while the compose / Makefile fallback is `biu_core`. When `.env` is present, `.env` wins and nothing breaks; only if you delete that line does it fall back to `biu_core`.
2. **External object storage credential variables don't take effect**: the `.env.example` comments suggest setting `S3_ACCESS_KEY` / `S3_SECRET_KEY` / `S3_BUCKET` / `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY`, but compose actually derives these envs from `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` / `MINIO_BUCKET` (`S3_ACCESS_KEY: ${MINIO_ROOT_USER:-biumind}` etc.) — setting them directly in `.env` has no effect. To connect external S3 / OSS, besides the address-type variables (`S3_ENDPOINT` etc., which work directly), the credentials require editing the compose file.
3. **`NATS_URL` cannot be configured via `.env`**: it is hardcoded to `nats://nats:4222` in compose; switching to an external NATS requires editing compose.
4. **The brain file bucket name is hardcoded**: brain's `MINIO_BUCKET` is hardcoded to `biumind-files` (unrelated to the main bucket's `MINIO_BUCKET` variable); renaming the main bucket does not affect it.
5. **`COMPOSE_PROJECT_NAME`**: the compose file already has `name: biumind` at the top, so listing this in `.env.example` is redundant (the two agree; no practical impact).
6. **`BIUMIND_LOG_LEVEL` example value**: the example gives `debug` while the compose fallback is `info` — a stack deployed from `.env.example` defaults to debug logging; `info` is recommended for production.

All remaining `.env.example` variables correspond one-to-one with compose references; there are no "written but never read" entries (`COMPOSE_PROJECT_NAME` is a built-in compose CLI variable and a normal exception).
