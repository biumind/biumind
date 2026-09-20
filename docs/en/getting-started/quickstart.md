# Quickstart

Pick one of two paths:

- **Path A · Cloud**: sign up and go, up and running in minutes — good for individuals and small teams.
- **Path B · Self-hosted trial**: use Docker Compose to bring up the full service stack on your machine — good if you want to see the whole picture first, or want your data on your own servers.

---

## Path A: Cloud (sign up and go)

1. Open the official download page at [biumind.ai/download](https://biumind.ai/download) and grab the client for your platform (macOS installer, Android APK, etc. — the page auto-detects your system and highlights the recommended download).

   Don't want to install anything? Just open the [Web version](https://biumind.ai/app) in your browser.

2. On first launch, **register an account** on the login page (email + password).

3. Once you're in, start anywhere: have a chat with the AI, or create a Wiki project and write your first document.

> [!TIP]
> If the desktop app warns about an "unidentified developer" or unsigned package on first open, right-click the app and choose "Open" on macOS, or click "Open Anyway" under System Settings → Privacy & Security. See [Download & Install](download.md) for details.

For download channels and installation caveats per platform, see [Download & Install](download.md).

## Path B: Self-hosted trial

Use the Docker Compose stack bundled with the repository to bring up the complete service set on your machine (Postgres / MinIO / NATS + all backend services + the web frontend + async workers), fronted by a local nginx gateway as the single entry point.

### Prerequisites

- Docker and the Docker Compose plugin (v2)
- At least ~4 GB of free memory
- Access to a container registry (the stack pulls CI pre-built images from an Aliyun registry by default — no local builds needed)

### Steps

```bash
# 1. Clone the repository
git clone https://github.com/biumind/biumind.git
cd biumind/deploy/docker-compose

# 2. Generate the environment configuration
cp .env.example .env

# 3. Bring up the full stack (infra + all services + frontend + workers)
make up

# 4. Once all services are up, check their health
make health
```

`make up` automatically waits for every container to pass its health check. Then open:

```text
http://localhost:8088
```

This is the local single entry point (the static site + the `/v1/*` API gateway). The web client lives at the `/app` path — register an account there on first visit. To point the desktop client at this self-hosted environment, set "Server address" in settings to `http://localhost:8088` — every service endpoint is served from this one address.

> [!WARNING]
> The passwords, JWT keys, and `BIUMIND_MASTER_KEY` in `.env.example` are placeholders. They're fine for a local trial, but **must all be replaced with strong random values before any externally reachable deployment**. Also note: this compose stack is an example setup for dev / test environments — hardening and operations for production deployment are your responsibility. See the [Self-hosting guide](../self-hosting/index.md).

### Common commands

```bash
make ps                 # List the status of all containers
make tail SVC=brain     # Follow the logs of a service (e.g. model-relay / identity / brain)
make psql               # Open a Postgres shell
make down               # Stop containers (data volumes are kept)
make clean              # Stop containers and delete all data volumes (asks twice; wipes all data)
```

> [!TIP]
> The `sandbox` (cloud sandbox, requires K8s) and `deploy` (one-click deployment) services are not part of this local stack. If you want to modify backend code and run it, build images locally with `make build-images` and start the stack with `make up-local`.

## Meet the biu CLI in 30 seconds

`biu` is BiuMind's terminal AI coding agent — it shares the same kernel and sessions as the graphical clients. Install on macOS / Linux with one command:

```bash
brew install biumind/tap/biu
```

Initialize the configuration (an interactive wizard walks you through three modes — cloud, self-hosted, or direct API key; the cloud mode uses browser-based authorization login):

```bash
biu init
```

Then jump straight into the REPL and get to work:

```bash
biu doctor    # Check that your environment and configuration are healthy
biu           # Enter the REPL; type /help for the command list
```

For more (configuration, permissions, memory files, command reference), see the [CLI getting started guide](../cli/getting-started.md).

## Next steps

- [Download & Install](download.md) — install channels for every platform
- [What is BiuMind](index.md) — an overview of the six modules
- [Self-hosting guide](../self-hosting/index.md) — production deployment and the environment variables reference
