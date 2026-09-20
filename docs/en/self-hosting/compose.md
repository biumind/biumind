# Local Development Stack (compose)

The compose stack in `deploy/docker-compose/` is positioned as a **starting example for development / test environments (dev/test only)**: it lets contributors bring up a complete environment with one command, or start only the infrastructure and keep the business services on the host for hot reload.

> [!NOTE]
> Relationship to the [deployment guide](index.md): the deployment guide is for self-hosting operators — running the full stack on pre-built images for the long term; this document is for local development — the most common workflow is `make up-infra` to start only Postgres / MinIO / NATS, `go run` the Go services from an IDE, and build the frontends locally as needed. Both use the same `docker-compose.yml` and `.env`; for variable descriptions see the [environment variables reference](env-vars.md).

> [!WARNING]
> The compose files in this directory trade security for development / testing convenience (all default ports exposed, `.env.example` ships dev secrets). Before using them directly as a production setup, complete the secret replacement, TLS termination, and port lockdown described in the [deployment guide](index.md) — deploying, hardening, and operating a production environment is your own responsibility.

---

## Quick Start

```bash
cd deploy/docker-compose
cp .env.example .env        # ⚠️ Replace every *_change_me placeholder with a strong random value
make up-infra               # Start only the infrastructure (Postgres / MinIO / NATS) — the most common local dev setup
# or
make up                     # Full stack (infra + all Go services + frontends + workers)
```

`make up` / `make up-infra` automatically runs `make wait-healthy` (up to 90 seconds waiting for all health checks to pass). A bare `docker compose up -d` is equivalent to `make up`.

---

## All make Targets

`.DEFAULT_GOAL` is `help`, so running plain `make` prints a cheat sheet. The complete list:

### Startup

| Target | Purpose |
|--------|---------|
| `make up-infra` | Start only the infrastructure (postgres / minio / nats / minio-bootstrap) — local development: services run on the host |
| `make up` | Start the full stack (infra + all Go services + frontends + workers), then automatically runs `wait-healthy` and prints the site entry address |
| `make up-workers` | Start only the workers (worker-aigc / worker-wiki-parse; required infra is pulled up automatically via `depends_on`) |
| `make up-all` | Alias: full stack (= `up`) |

### Stop and Cleanup

| Target | Purpose |
|--------|---------|
| `make down` | Stop containers (data volumes are kept) |
| `make clean` | ⚠️ Stop containers and delete **all** volumes (all data wiped; interactively requires typing `yes` to confirm) |
| `make restart` | `down` + `up` |
| `make fresh` | One-shot rebuild (`clean` + `up-infra`: wipe all data, then restart the infra) |

### Status and Logs

| Target | Purpose |
|--------|---------|
| `make ps` | List containers and health status |
| `make logs` | Follow all service logs (`--tail=100`, exit with CTRL-C; accepts `SVC=<name>`) |
| `make tail SVC=<name>` | Follow a single service's logs (e.g. `make tail SVC=model-relay`; `SVC` is required) |
| `make health` | `curl` the `/healthz` on ports 7001/7002/7003/7004/7007/7008/7009 one by one |
| `make wait-healthy` | Wait for all health checks to pass (up to 90 seconds; on timeout prints `ps` and exits non-zero) |

### Images

| Target | Purpose |
|--------|---------|
| `make build-images` | Build all Go service images used by compose locally (`docker.io/biumind/<name>:dev`); pairs with `up-local` |
| `make up-local` | Start the stack using the local `build-images` artifacts (equivalent to `BIUMIND_REGISTRY=docker.io BIUMIND_TAG=dev make up`) |
| `make pull-images` | Pull the external dependency images (`postgres` / `minio` / `nats`) |
| `make authz-eval` | Test one authz decision: `make authz-eval PRINCIPAL=user:u1 ACTION=wiki:Page::read RESOURCE=page:p1` (requires `jq`) |

> [!NOTE]
> `make build-images` builds the 9 Go services one by one (aigc / app-center / authz / brain / channels / identity / model-relay / realtime / runtime); the build context is always the **repository root** `../..` — every service Dockerfile needs to `COPY packages/go-sdk/biu` (brain / runtime also COPY `apps/cli/biu`), so a narrow context makes the COPY fail. Note that the directory name `app_center` differs from the image name `app-center`; the Makefile handles this.

### Database

| Target | Purpose |
|--------|---------|
| `make psql` | Open a Postgres shell (uses `POSTGRES_USER` / `POSTGRES_DB` from `.env`) |
| `make backup-pg` | `pg_dump` backup (custom format) into `./backups/` |
| `make restore-pg FILE=backups/xxx.dump` | Restore from a backup (`pg_restore --clean --if-exists`; `FILE` is required) |

### Utilities and Validation

| Target | Purpose |
|--------|---------|
| `make nats-streams` | List NATS JetStream streams |
| `make minio-console` | Open the MinIO console (default `http://localhost:9001`) |
| `make lint` | Validate the compose file syntax with `docker compose config -q` |
| `make check-env` | Gatekeeper (a dependency of `up*`): checks that `.env` exists and `BIUMIND_MASTER_KEY` is set |

The Makefile automatically does `include .env` and `export`s it, so standalone commands like `make psql` also pick up the variables from `.env`.

---

## Startup Scope and Profiles

By default (bare `docker compose up -d` / `make up`) this starts: 4 infrastructure containers + 9 Go services + 4 frontends + 2 Python workers. Two services sit behind optional profiles and are **not** started by default:

| Profile | Service | Start command | Notes |
|---------|---------|---------------|-------|
| `ocr` | `mineru` | `docker compose --profile ocr up -d mineru` | Self-hosted OCR (PDF parsing). No official pre-built image; build it yourself and point to it via `MINERU_IMAGE`; enabling also requires setting `WIKI_PARSE_OCR_ENABLED=true` in `.env` |
| `llm` | `worker-wiki-llm` | `docker compose --profile llm up -d worker-wiki-llm` | The LLM generation stage of the wiki ingestion pipeline; the only worker in the stack that actively spends API credits, hence off by default |

For any other subset, just name the services explicitly, e.g. `docker compose up -d postgres minio nats`. The `sandbox` / `deploy` services are not in compose (they depend on K8s / a standalone environment).

---

## Local Development Mode (Most Common)

Run only the infrastructure and keep the business services on the host (code changes take effect immediately):

```bash
make up-infra
```

All services load their configuration from 12-factor environment variables (no config files). To run a service on the host, export the env group from the compose `x-svc-env` anchor with the addresses swapped to `localhost` — for example, running model-relay:

```bash
cd services/model-relay
export BIUMIND_ENV=test
export DATABASE_URL='postgres://biumind:<password>@localhost:5432/biumind?sslmode=disable'
export NATS_URL='nats://localhost:4222'
export S3_ENDPOINT='http://localhost:9000'
export S3_ACCESS_KEY=biumind S3_SECRET_KEY='<MinIO password>' S3_BUCKET=biumind
export JWT_SECRET='<same as .env>' JWT_ISSUER='https://identity.biumind.local' JWT_AUDIENCE='biumind-api'
export IDENTITY_JWKS_URL='http://localhost:7004/.well-known/jwks.json'
export LISTEN_ADDR=':7001' SERVICE_NAME=model-relay
go run ./cmd/model-relay
```

> [!NOTE]
> In the example above, the database name `biumind` matches `POSTGRES_DB=biumind` from `.env.example`; the fallback value in the compose file and Makefile, used when the variable is unset, is `biu_core`. As long as `.env` exists, `.env` wins — see the [environment variables reference](env-vars.md) for details.

Local builds and integration testing of the three SPA frontends and site belong to the repository's development workflow (`task` tasks are in `Taskfile.yml` at the repo root); the compose-side counterpart is `make build-images` + `make up-local`, which starts the full stack on images built from local code for verification.

---

## FAQ

**Q: `make up` reports unhealthy / `wait-healthy` times out**
A: Start with `make logs SVC=postgres`; most often the initialization SQL failed (see `postgres/init/*.sql`), or a service never sees its dependency become healthy — follow the unhealthy containers in `make ps` and check their logs.

**Q: I edited `postgres/init/*.sql` but nothing changed**
A: Postgres runs the init scripts only when the data volume is empty. Rebuild with `make clean && make up-infra` (or `make fresh`).

**Q: the `identity` container crash-loops, logs show `permission denied: jwt-signing-key.pem`**
A: identity runs as a non-root user; if the `identity-keys` volume was created with an older image (root-owned), it cannot write the key. Delete the volume and recreate it:

```bash
docker compose down identity
docker volume rm biumind_identity-keys
make up
```

**Q: I want to test against a real LLM provider**
A: After deployment, open the admin console (`http://localhost:8088/admin/`) and fill in upstream credentials under "Model Configuration" (stored encrypted at rest via envelope encryption). The `BIUMIND_ANTHROPIC_KEY` in `.env` is only a fallback direct channel for the runtime.

**Q: the websearch tool doesn't work**
A: The compose stack ships no search instance. Deploy a SearxNG yourself, then set `SEARXNG_URL=http://<host>:8080` in `.env` and recreate the brain container to enable it.

**Q: file-descriptor errors on macOS**
A: `ulimit -n 65536`, or raise the limit in the Docker Desktop settings.

---

## Next Steps

- Self-hosting deployment with pre-built images: [deployment guide](index.md)
- Descriptions of all `.env` variables: [environment variables reference](env-vars.md)
- Design decisions of the compose stack: `deploy/docker-compose/DESIGN.md` in the repository
