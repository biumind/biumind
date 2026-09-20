# Self-Hosting Deployment

BiuMind supports private deployment: all backend services, frontends, and async workers ship as container images, with pre-built images continuously published by official CI. The basic self-hosting path is: **clone the repo → configure `.env` → pull the images → bring up the whole stack with `docker compose`**.

> [!NOTE]
> How the three documents divide the work:
>
> - **This document**: for self-hosting operators — how to run the full BiuMind stack on pre-built images, plus gateway routing, verification, backup, and upgrades.
> - [Local development stack (compose)](compose.md): for contributors and secondary development — starting only the infrastructure, running services on the host, building images locally, and similar workflows.
> - [Environment variables reference](env-vars.md): the full `.env` variable reference, including the results of cross-checking against the compose file.

---

## Prerequisites

- **Docker Engine + Docker Compose v2** (the `docker compose` subcommand form; the repo does not provide a bare `docker-compose` v1 shim).
- **A git clone of this repository**. Besides the images, the compose stack also bind-mounts several files from the repo that are required at runtime: the Postgres initialization SQL (`deploy/docker-compose/postgres/init/`), the NATS configuration (`deploy/docker-compose/nats/nats.conf`), the Authz authorization policies (`deploy/docker-compose/authz/policies/`), and the Runtime built-in Skills (`packages/skills-stdlib/`). So even a pure image-pull deployment with no local builds still needs a checkout of the repository.
- `make` and `curl` (required by the Makefile convenience commands); `jq` (only needed for `make authz-eval`).
- The machine must be able to reach the image registry of your choice (see [Image registry](#4-choose-an-image-registry) below).
- The ports in the table below must be free (all of them can be changed in `.env`):

| Service | Default port | Notes |
|---------|--------------|-------|
| **site (unified entry point)** | **8088** | The single entry point for clients, `http://localhost:8088` |
| Postgres | 5432 | Database (pgvector image) |
| MinIO | 9000 / 9001 | Object storage API / Console |
| NATS | 4222 / 8222 | Message queue client / monitor |
| model-relay | 7001 | Model gateway (the single egress for all model calls) |
| runtime | 7002 | Agent runtime engine |
| brain | 7003 | Knowledge layer (Wiki / Graph / Memory / Search) |
| identity | 7004 | Accounts, quotas, BYOK |
| channels | 7007 | Multi-channel IM gateway (optional, requires a bot token) |
| realtime | 7008 | Realtime push (SSE) |
| authz | 7009 | Unified authorization policy engine |
| app-center | 7011 | App Center |
| aigc | 7012 | Text-to-image / video / digital humans |

> [!NOTE]
> The three frontend containers — `web-client` / `admin-web` / `miniapp-h5` — do **not** bind host ports; they are exposed only through the site gateway by path (`/app`, `/admin`, `/m`). The `sandbox` and `deploy` services have CI images but are **not part of the compose stack** (they depend on K8s and a standalone runtime environment respectively); pull them yourself if needed.

---

## Architecture Overview

The stack consists of four layers (all inside the same `biu-net` docker network, reachable from each other by container name):

```text
Clients (desktop / web / mini-program / CLI)
        │  Single origin: only one "server address" is configured = the site address
        ▼
site (nginx, :8088)  ──  /v1/* reverse-proxies the backends; /app /admin /m reverse-proxy the SPAs; static marketing site + /docs documentation
        │
        ├── identity (:7004)      Accounts / billing / BYOK
        ├── authz (:7009)         Cedar authorization policies
        ├── model-relay (:7001)   Model gateway (BYOK + platform pool + billing)
        ├── brain (:7003)         Knowledge layer
        ├── runtime (:7002)       Agent runtime
        ├── realtime (:7008)      SSE realtime push
        ├── app-center (:7011)    App Center
        ├── channels (:7007)      Multi-channel IM (optional)
        ├── aigc (:7012)          AIGC access layer
        └── workers (Python)      Async jobs: worker-aigc / worker-wiki-parse (+ optional worker-wiki-llm)

Infrastructure: Postgres 16 (pgvector) / MinIO / NATS JetStream 2.10
```

**Single-origin addressing** is the core constraint of the BiuMind client: the client configures exactly one server address (`http://localhost:8088` by default locally), all API calls go to site, and its nginx reverse-proxies them by path to the corresponding backend. This also means that once deployed, you only need to **expose a single port — site's** to the outside world; every other service port can stay bound to the internal network or be left unpublished entirely.

---

## Deployment Steps

### 1. Clone the repository and enter the compose directory

```bash
git clone https://github.com/biumind/biumind.git
cd biumind/deploy/docker-compose
```

### 2. Generate the configuration file

```bash
cp .env.example .env
```

### 3. Change the required secrets

Every value in `.env` containing `change_me` or `dev` must be replaced with a strong random value. At minimum:

| Variable | Description | How to generate |
|----------|-------------|-----------------|
| `BIUMIND_MASTER_KEY` | Master encryption key for services (`make` enforces a check; startup is refused outright if it is missing) | `openssl rand -base64 32` |
| `JWT_SECRET` | JWT signing key (no default in compose; must be set) | A random string of 32+ characters |
| `POSTGRES_PASSWORD` | Postgres password (compose ships a dev default; make sure to replace it) | Strong random value |
| `MINIO_ROOT_PASSWORD` | MinIO password (compose ships a dev default; make sure to replace it) | Strong random value |
| `IDENTITY_INTERNAL_TOKEN` | Shared bearer for internal service-to-service calls (must be identical across services; make sure to replace it) | Strong random value |
| `BYOK_MASTER_KEY` | Master key encrypting user BYOK keys; leave empty to disable BYOK entirely | `openssl rand -base64 32` |

> [!WARNING]
> Do not rotate these secrets casually once they are in use: after rotating `BIUMIND_MASTER_KEY` / `BYOK_MASTER_KEY`, old ciphertexts can no longer be decrypted; the `identity-keys` volume also holds RSA signing keys auto-generated on first startup — deleting that volume invalidates every token already issued.

Configure the remaining variables (model preferences, channel tokens, OCR, etc.) as needed; see the [environment variables reference](env-vars.md) for item-by-item descriptions.

### 4. Choose an image registry

Business images are uniformly `${BIUMIND_REGISTRY}/biumind/<name>:${BIUMIND_TAG}` — the namespace is fixed at `biumind`, only the host changes. Pick one of the three options and write it into `.env`:

| Source | `.env` settings |
|--------|-----------------|
| Aliyun Beijing (default, fast in mainland China) | Don't set `BIUMIND_REGISTRY`, set `BIUMIND_TAG=main` |
| GitHub GHCR | `BIUMIND_REGISTRY=ghcr.io`, `BIUMIND_TAG=main` |
| Docker Hub | `BIUMIND_REGISTRY=docker.io`, `BIUMIND_TAG=main` |

Allowed values for `BIUMIND_TAG`: `main` (updated on every push to the main branch), `v*` (release version numbers), `sha-<short-hash>` (pins a specific commit). Infrastructure images (Postgres / MinIO / NATS) are controlled by `INFRA_REGISTRY`, which defaults to `docker.io`.

> [!NOTE]
> Packages on GHCR must be public to be pulled anonymously. If pulls time out, you can set `INFRA_REGISTRY` separately for the infrastructure images to point at a reachable image registry.

### 5. Pull the images and start

```bash
docker compose pull   # Pull first, so compose doesn't fall back to a local build
make up               # = docker compose up -d + waits for health checks to pass
```

When `make up` finishes, it prints the unified entry point address (default `http://localhost:8088`).

### 6. Verify

See [Health verification](#health-verification) below.

---

## Startup Scope and Profiles

The vast majority of services in the compose file carry **no profile**: a bare `docker compose up -d` (i.e. `make up`) starts the full stack by default. Only two services sit behind optional profiles:

| Goal | Command |
|------|---------|
| Full stack (default) | `make up` / `docker compose up -d` |
| Infrastructure only | `make up-infra` (Postgres / MinIO / NATS + bucket initialization) |
| Workers only | `make up-workers` (required infra is pulled up automatically via `depends_on`) |
| Add the MinerU OCR service (profile `ocr`) | `docker compose --profile ocr up -d mineru` |
| Add the wiki-llm worker (profile `llm`) | `docker compose --profile llm up -d worker-wiki-llm` |
| Any subset | Name the services explicitly, e.g. `docker compose up -d postgres minio nats` |

> [!WARNING]
> The `worker-wiki-llm` in the `llm` profile is the only worker in the stack that actively spends LLM API credits (it generates Wiki pages from external-source text), which is why it is not started by default — enable it explicitly only once you are sure you need it and the model is configured. The `mineru` service in the `ocr` profile has no official pre-built image; you must build it yourself and point to it via `MINERU_IMAGE` (see the [environment variables reference](env-vars.md) for details).

Startup ordering is guaranteed automatically by `depends_on` + health checks: all Go services wait for Postgres / NATS to be healthy; model-relay waits for identity / authz; brain waits for model-relay; runtime waits for brain; site waits for the three SPA frontends. `make up` internally calls `make wait-healthy` (waits up to 90 seconds).

---

## Health Verification

Every service container has a health check configured (Go services expose `/healthz` on their own port; frontend containers use `http://localhost/healthz`). Common verification commands:

```bash
make ps                 # List all containers and their health status
make health             # curl each backend /healthz (7001/7002/7003/7004/7007/7008/7009)
make tail SVC=model-relay   # Follow a single service's logs (make logs follows all of them)
curl http://localhost:8088/healthz   # site gateway health check
```

> [!NOTE]
> `make health` currently probes only those 7 ports, 7001–7009 — **it does not cover app-center (7011), aigc (7012), or site (8088)**. For those three, check container health status with `make ps`, or verify manually with `curl http://localhost:7011/healthz` and `curl http://localhost:7012/healthz`.

Verify in the browser:

| Address | Content |
|---------|---------|
| `http://localhost:8088/` | Static marketing site |
| `http://localhost:8088/app/` | Web client (Flutter Web) |
| `http://localhost:8088/admin/` | Admin console (Vue) |
| `http://localhost:8088/m/` | Mini-program H5 version |
| `http://localhost:8088/docs/` | Online documentation |

On first use, register an account in the web client; for model calls, fill in upstream provider credentials under "Model Configuration" in the admin console (stored encrypted at rest via envelope encryption — not through environment variables).

---

## The site Gateway and the `/v1/*` Route Table

The nginx inside the site container (`web/site/nginx.conf`) plays a dual role: static marketing site + API gateway. Every API call from the single-origin client is routed by path through it:

| Upstream service | Route prefixes |
|------------------|----------------|
| identity (:7004) | `/v1/auth/`, `/v1/identity/`, `/oauth/`, `/.well-known/oauth-authorization-server`, `/v1/admin/` (fallback), `/v1/billing/webhook`, `/v1/plans`, `/v1/subscriptions`, `/v1/coupons`, `/v1/referrals`, `/v1/credits`, `/v1/announcements` |
| model-relay (:7001) | `/v1/messages`, `/v1/admin/providers`, `/v1/admin/credentials`, `/v1/admin/models`, `/v1/admin/channels`, `/v1/admin/pricing`, `/v1/admin/fx-rates`, `/v1/admin/model-groups`, `/v1/chat/estimate`, `/v1/me/usage`, `/v1/me/models`, `/v1/audio/` |
| brain (:7003) | `/v1/agent/`, `/v1/memory`, `/v1/threads`, `/v1/providers`, `/v1/mcp`, `/v1/wiki/`, `/v1/notes`, `/v1/notebooks`, `/v1/note-tags`, `/v1/shares` (public share endpoints, rate-limited), `/v1/graph/`, `/v1/search`, `/v1/chat/stats`, `/v1/chat/search`, `/v1/chat/tombstones`, `/v1/tools`, `/v1/files`, `/v1/brain/` |
| runtime (:7002) | `/v1/agents`, `/v1/skills` |
| realtime (:7008) | `/v1/realtime/` (long-lived SSE) |
| authz (:7009) | `/v1/authz/` |
| aigc (:7012) | `/v1/models`, `/v1/gallery`, `/v1/generations`, `/v1/characters`, `/v1/voices`, `/v1/aigc/` |
| app-center (:7011) | `/v1/apps`, `/v1/sidebar` |
| SPA frontends | `/app/` → web-client, `/admin/` → admin-web, `/m/` → miniapp-h5 |
| Static site | `/`, `/_astro/*`, `/s/` (share landing pages), `/docs/` (online documentation), `/downloads/*` (client installers) |

A few gateway behaviors worth knowing:

- **`/v1/internal/*` is unconditionally `deny all` (403)** — internal service-to-service endpoints are never exposed to the public network and can only be called inside the docker network.
- **`/v1/admin/*` splits by longest prefix**: the 7 exact model-relay prefixes win first; everything else falls through to identity.
- **WebSocket and streaming endpoints** (`/v1/wiki/` sync WS, `/v1/agent/` session WS, `/v1/messages` streaming responses, `/v1/realtime/` SSE) have proxy buffering disabled and upgrade headers plus long timeouts configured.
- **Upload limits**: `/v1/files` allows 110 MB (aligned with the brain server-side 100 MB limit + multipart overhead); `/v1/messages` and `/v1/agent/` allow 20 MB (base64 inline images).
- **Public share endpoint rate limits**: `/v1/shares` at 30 req/min (burst 20), the unlock endpoint at 10 req/min (burst 5); exceeding the limit returns 429.

> [!WARNING]
> If you add a new `/v1/...` endpoint to a backend during secondary development, you must add the matching `location` reverse-proxy entry in `web/site/nginx.conf`, otherwise the request falls through to the static site and returns 404 — the feature fails silently. If a self-hosted user hits a 404 on a client feature, check the gateway routing first.

### HTTPS

The site container listens only on plain HTTP port 80 (mapped externally to 8088). **TLS is terminated by a fronting layer**: for test environments, a tunnel such as frp works; for production, put a public load balancer / Nginx Proxy Manager / CDN in front and forward 443 to site's 8088. In production you should also restrict how many backend service ports are exposed externally (under the single-origin architecture, only site needs to be exposed).

---

## Data Persistence and Backup

The compose stack uses named volumes (project name `biumind`; docker adds the prefix automatically):

| Volume | Contents |
|--------|----------|
| `biumind_postgres-data` | All Postgres business data (each service has its own schema) |
| `biumind_minio-data` | MinIO objects (raw documents / media / build artifacts) |
| `biumind_nats-data` | NATS JetStream persistence |
| `biumind_identity-keys` | Identity RSA signing keys (generated on first startup; keeps tokens valid across restarts) |
| `biumind_app-center-data` | App Center built-in tasks App files |
| `biumind_mineru-models` | MinerU model cache (`ocr` profile only) |

Common operations:

```bash
make backup-pg                    # pg_dump backup into ./backups/ (custom format)
make restore-pg FILE=backups/xxx.dump   # Restore from a backup
make down                         # Stop all containers (volumes are kept; data untouched)
make clean                        # ⚠️ Stops and deletes ALL volumes — all data is wiped; asks for confirmation
```

> [!WARNING]
> `make clean` deletes every volume including the Postgres data, irrecoverably. In production, always run `make backup-pg` first to keep an archive. The Postgres `postgres/init/` initialization SQL runs only when the data volume is empty — editing the init scripts does not affect existing data.

The MinIO buckets (the main bucket, snapshots, deploy, the five AIGC buckets, releases, etc.) are created automatically on first startup by the one-shot `minio-bootstrap` job, which also configures the anonymous download policies and lifecycle rules — no manual work required.

---

## Upgrading

```bash
cd deploy/docker-compose
# 1. Change BIUMIND_TAG in .env (e.g. switch from main to a fixed version v0.x.x, or stay on main to track rolling updates)
# 2. Pull the new images and recreate the changed containers
docker compose pull
docker compose up -d
```

Database schemas are migrated automatically by each service at startup via goose migrations — no manual action needed. Running `make backup-pg` before upgrading is recommended. To roll back, set `BIUMIND_TAG` back to the old version and run `pull + up -d` again (schema migrations generally only move forward; before rolling back across a major version, confirm first that your backup is restorable).

---

## Next Steps

- For item-by-item configuration, see the [environment variables reference](env-vars.md).
- To run a local development environment (services on the host with hot reload, locally built images), see [Local development stack (compose)](compose.md).
- For internals of each service, see `services/<name>/` in the repository.
