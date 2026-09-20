# `biu` command reference

`biu` is BiuMind's command-line tool: talk to AI in the terminal, have AI read and write code and files, register your machine as a remotely schedulable execution environment, and manage plugins, skills, and apps.

This page covers all of `biu`'s subcommands and flags. Slash commands inside the REPL (`/help`, `/model`, etc.) are covered in the [top-level usage](#top-level-usage-biu--repl) section.

> [!TIP]
> Top-level flags (such as `--model`, `--token`, `--config`) are equally available on all subcommands — no need to worry about whether they go before or after the subcommand.

## Table of contents

- [Quick start](#quick-start)
- [Configuration and credential locations](#configuration-and-credential-locations)
- [Top-level usage (biu / REPL)](#top-level-usage-biu--repl)
- [Command overview](#command-overview)
- [Account and initialization](#account-and-initialization)
  - [`biu init`](#biu-init) · [`biu auth`](#biu-auth) · [`biu pair`](#biu-pair)
- [Diagnostics](#diagnostics)
  - [`biu doctor`](#biu-doctor) · [`biu version`](#biu-version)
- [Sessions, plans, and usage](#sessions-plans-and-usage)
  - [`biu sessions`](#biu-sessions) · [`biu plan`](#biu-plan) · [`biu usage`](#biu-usage)
- [Configuration management](#configuration-management)
  - [`biu config`](#biu-config)
- [MCP servers](#mcp-servers)
  - [`biu mcp`](#biu-mcp)
- [Knowledge base ingestion](#knowledge-base-ingestion)
  - [`biu ingest`](#biu-ingest)
- [Daemon and remote scheduling](#daemon-and-remote-scheduling)
  - [`biu serve`](#biu-serve) · [`biu bridge`](#biu-bridge) · [`biu agent worker`](#biu-agent-worker)
- [Plugins and skills](#plugins-and-skills)
  - [`biu plugin`](#biu-plugin) · [`biu skill`](#biu-skill)
- [App development and local running](#app-development-and-local-running)
  - [`biu app`](#biu-app) · [`biu repo-app`](#biu-repo-app)
- [Environment variables](#environment-variables)

## Quick start

```bash
# 1. Interactive initialization (writes ~/.biu/config.toml and walks you through browser login)
biu init

# 2. Self-check
biu doctor

# 3. Enter the interactive REPL
biu

# 4. One-shot prompt (non-interactive; stdout is a JSONL event stream)
biu --headless --json --prompt "Explain what this code does"
```

Running `biu` for the first time requires a default model: set `[default].model` in `~/.biu/config.toml` (running `biu init` guides you through it), or pass `--model <id>` on each run.

## Configuration and credential locations

| Path | Contents |
|------|----------|
| `~/.biu/config.toml` | Main configuration (deployment mode, model, endpoints, tokens). Recommended permission 0600 |
| `~/.biu/auth.json` | File-fallback storage for the OAuth token (used only when the system has no keychain; 0600) |
| System keychain | Preferred storage for OAuth and device tokens: macOS Keychain / Linux Secret Service (service name `com.biumind.biu`) |
| `~/.biu/sessions/` | Session JSONL logs, bucketed by project directory |
| `~/.biu/plans/` | Plan files (output of `ExitPlanMode`) |
| `~/.biu/usage.jsonl` | Token usage records (the data source for `biu usage`) |
| `~/.biu/telemetry.json` / `~/.biu/telemetry.jsonl` | Telemetry toggle file / event log (off by default) |
| `~/.biu/update-check.json` | State file for the startup update check |
| `~/.biu/logs/daemon.log` | `biu serve` daemon log (auto-truncated past 10MB) |
| `~/.biu/device_token` | File-fallback storage for the device token (0600; stored in the keychain when one is available) |
| `~/.biumind/settings.json` | Layered settings (permissions, hooks, status line, sandbox, plugin disable list, etc.) |
| `~/.biumind/skills/` | Local skills directory (`SKILL.md`) |
| `~/.biumind/plugins/` | Installed plugins directory |
| `~/.biumind/repo-apps/` | repo-app instances directory |
| `~/.biumind/keys/` | App publisher signing keys |

> [!NOTE]
> Tokens obtained via OAuth login are written to the system keychain first; in environments without a keychain (some Linux servers) biu falls back to `~/.biu/auth.json` (0600). Use `biu auth status` to see which storage backend is actually in effect, and `biu auth migrate` to move tokens from the legacy file into the keychain.

## Top-level usage (biu / REPL)

Running `biu` without a subcommand enters the interactive REPL; with `--headless` / `--json` it answers a single prompt and exits.

```bash
biu                        # interactive REPL
biu --headless --prompt "…"   # one-shot prompt, plain-text output
biu --json --prompt "…"       # one-shot prompt, JSONL event stream on stdout
biu --resume <session-id>     # resume a specific session (replays the event log)
biu --continue                # resume the most recent session of the current project
```

### Top-level flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--config` | string | `""` | Path to config.toml (default `~/.biu/config.toml`, or `BIU_CONFIG`) |
| `--headless` | bool | `false` | Non-interactive one-shot mode |
| `--json` | bool | `false` | Output a JSONL event stream on stdout (implies `--headless`) |
| `--prompt` | string | `""` | Prompt text (headless mode); if empty, read from stdin |
| `--model` | string | `""` | Override the default model (takes precedence over `[default].model`) |
| `--system` | string | `""` | System prompt prefix |
| `--model-relay-url` | string | `""` | Override the model-relay endpoint |
| `--token` | string | `""` | Override the bearer token |
| `--mode` | string | `""` | Deployment mode: `cloud` \| `byo_endpoint` \| `direct` (overrides the config file) |
| `--no-log` | bool | `false` | Disable JSONL session logging |
| `--resume` | string | `""` | Session id to resume (replays that session's event log into the engine) |
| `--continue` | bool | `false` | Resume the most recent session in the current project directory (ignored when `--resume` is set) |
| `--fork-session` | bool | `false` | Explicitly fork from `--resume`; effectively a no-op — biu always replays into a new session id, and the original session file is never overwritten |
| `--rewind-files` | string | `""` | Restore files to their state before the given user message (UUID), then exit; requires `--resume` or `--continue` |
| `--permission-policy` | string | `deny` | headless / SDK permission policy: `deny` (default — deny and fail) \| `allow` \| `stdin` (interactive prompts in the terminal) \| `stdin-json` (for GUIs: PERMISSION_ASK events on stdout, JSON decisions read from stdin) |
| `--permission-mode` | string | `""` | Engine permissions mode: `default` \| `acceptEdits` \| `bypassPermissions`; if empty, falls back to `defaultMode` from settings.json |
| `--add-dir` | string[] | — | Extra working directories the model may read and write; repeatable, comma-separated values also accepted; effective for this run only (not persisted) |

> [!WARNING]
> The default permission policy in headless mode is `deny`: unattended tool calls are denied with an error instead of hanging. For automation, choose explicitly: `--permission-policy=allow` (open up) or `stdin` / `stdin-json` (hand the decision over).

### REPL overview

Typing `/` inside the REPL opens the slash-command list; `/help` shows everything. Common commands:

| Command | Description |
|---------|-------------|
| `/help` | Show all slash commands |
| `/model <id>` | Switch the model for this session |
| `/mode <mode>` | Switch permissions mode (`default` / `acceptEdits` / `plan` / `bypass`) |
| `/compact` | Compact the conversation history to save context |
| `/clear` | Clear history and start over |
| `/resume [#n\|latest\|<id>]` | Replay a saved session |
| `/sessions` | List recent sessions |
| `/export <path>` | Export the current session as md / json / anthropic-replay |
| `/cost [--by-tool]` | View this session's tokens and costs |
| `/usage [today\|week\|month\|all]` | View historical usage summaries |
| `/permissions` | View the permission rules and mode currently in effect |
| `/mcp [<server>]` | View connected MCP servers and their tools |
| `/plugin` | View / toggle plugins |
| `/memory [list\|reload]` | View BIUMIND.md files and auto-memory status |
| `/remember <text>` | Save a memory to `~/.biumind/memory` |
| `/agents [create <name>]` | List / create subagents |
| `/todo` | Print the in-session todo list |
| `/commit` / `/pr` | Stage and create a Conventional Commits commit / create a PR (requires `gh`) |
| `/doctor` | In-REPL health check |
| `/upgrade [run\|check]` | Upgrade biu itself |
| `/quit` | Quit |

The authoritative full list is the `/help` output inside the REPL.

## Command overview

`biu` has 18 top-level subcommands:

| Command | Purpose |
|---------|---------|
| [`biu init`](#biu-init) | Interactive initial configuration |
| [`biu auth`](#biu-auth) | OAuth login / logout / status / credential migration |
| [`biu pair`](#biu-pair) | Pair this machine to your BiuMind account (device token) |
| [`biu doctor`](#biu-doctor) | Comprehensive self-check |
| [`biu version`](#biu-version) | Print version information |
| [`biu sessions`](#biu-sessions) | View and export session logs |
| [`biu plan`](#biu-plan) | View and manage plan files |
| [`biu usage`](#biu-usage) | Summarize token usage |
| [`biu config`](#biu-config) | Inspect / validate configuration, output JSON Schemas, manage telemetry and update-check toggles |
| [`biu mcp`](#biu-mcp) | Inspect MCP servers and their tool catalogs |
| [`biu ingest`](#biu-ingest) | Parse local files and optionally commit them to a Wiki |
| [`biu serve`](#biu-serve) | Run as a daemon (bridge HTTP + optional remote-scheduling registration) |
| [`biu bridge`](#biu-bridge) | Expose the agent over HTTP/SSE for IDE / local UIs to drive |
| [`biu agent worker`](#biu-agent-worker) | Register this machine as an Agent Plane execution node |
| [`biu plugin`](#biu-plugin) | Plugin management (including marketplaces) |
| [`biu skill`](#biu-skill) | Skill management (local/cloud sync, packaging, signing) |
| [`biu app`](#biu-app) | Develop, package, and locally debug App Center BiuApps |
| [`biu repo-app`](#biu-repo-app) | Run GitHub open-source projects as local web services |

## Account and initialization

### `biu init`

Interactive initialization wizard: choose a deployment mode (`cloud` via the BiuMind cloud / `byo_endpoint` with your own model-relay / `direct` straight to the Anthropic API), collect credentials, and write `~/.biu/config.toml` (confirming before overwriting if one exists). The wizard is plain text throughout and works over SSH / in CI; every question can be replaced with a flag for scripting.

After writing the config it runs a connectivity smoke test; `--with-memory` / `--with-settings` can also generate a starter project memory file and permissions settings.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--mode` | string | `""` | Deployment mode: `cloud` \| `byo_endpoint` \| `direct` (skips the mode question) |
| `--api-key` | string | `""` | Anthropic API key (used with `--mode=direct`) |
| `--model-relay-url` | string | `""` | model-relay endpoint URL (`cloud` / `byo_endpoint` modes) |
| `--model-relay-token` | string | `""` | model-relay auth token (`cloud` / `byo_endpoint` modes) |
| `--model` | string | `""` | Default model (skips the question) |
| `--with-memory` | bool | `false` | Also generate a BIUMIND.md template in the current directory |
| `--with-settings` | bool | `false` | Also generate a starter `~/.biumind/settings.json` |
| `--yes` | bool | `false` | Skip all interactive questions and rely entirely on flags (overwriting an existing config no longer asks either) |

```bash
biu init                          # fully interactive
biu init --mode=direct --api-key sk-ant-… --yes   # scripted direct mode
```

> [!TIP]
> In cloud mode the wizard first walks you through browser OAuth login; the token goes into the system keychain, not the config file. When no browser is available (SSH etc.) you can paste a token instead, or run `biu auth login --manual` afterwards.

### `biu auth`

Manage OAuth credentials. OAuth endpoints are derived from `[model-relay].endpoint` by default (single-origin architecture); self-hosted environments can override them via the `[auth]` section of the config file or `BIU_OAUTH_*` environment variables.

#### `biu auth login`

Log in via browser OAuth (PKCE) and persist the token. By default it spins up a local callback port and completes automatically; `--manual` suits SSH / sandboxed environments: it prints the authorization URL, and you paste back the full redirect URL after approving to complete the exchange.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--manual` | bool | `false` | Manual paste flow (SSH / sandbox environments) |

#### `biu auth logout`

Revokes the remote refresh token (network failure only warns) and deletes the locally cached OAuth token. No flags.

#### `biu auth status`

Shows the login state: storage backend, token summary, scopes, expiry, and whether it is expired. No flags.

#### `biu auth migrate`

Moves tokens from the legacy `~/.biu/auth.json` into the system keychain in one shot. Idempotent: once migrated, the file is deleted, and re-running is a no-op. On hosts without a keychain it says so explicitly and leaves the file alone.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--dry-run` | bool | `false` | Only print the migration actions; write nothing |

### `biu pair`

Pairs this machine to your BiuMind account in exchange for a **restricted, revocable-at-any-time device token**, replacing the practice of placing a full account PAT on daemon machines. Flow: `biu pair` produces a pairing code → you enter and approve it on any logged-in device (phone / web) → this machine polls, receives the device token, and stores it in the keychain (fallback `~/.biu/device_token`, 0600). Afterwards `biu agent worker` / `biu serve` automatically prefer it. The pairing code is valid for 5 minutes.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--brain-url` | string | `""` | brain service URL (default: `BIUMIND_BRAIN_URL` or `[model-relay].endpoint`) |
| `--name` | string | `""` | Device name reported to the server (default: hostname) |

```bash
biu pair --brain-url https://your-biumind.example.com
```

## Diagnostics

### `biu doctor`

The first command to run when something goes wrong. It checks each item and prints a colorized list (✓ ok / ! degraded warning / ✗ failed); any failure ends with a non-zero exit code. Checks include:

- Config file loading and permissions (should be 0600)
- Deployment mode, model, permissions mode
- direct mode: provider / endpoint / connectivity, or cloud mode: model-relay `/healthz`
- `~/.biu` and `~/.biumind` directory layout and permissions
- External tools (`git` / `rg` / `gopls`) and sandbox tools (macOS `sandbox-exec`, Linux `bwrap`)
- Layered settings.json and the merged sandbox-rule result
- Auth storage backend, OAuth token expiry and refresh capability, token source (`--token` > `BIUMIND_TOKEN` > `[model-relay].virtual_key` > OAuth storage)
- Update-check state (reads local state only; no network request)

No flags of its own; top-level flags (such as `--config`, `--mode`, `--token`) affect the results.

### `biu version`

Prints version, commit, build time, Go version, and OS/arch. `biu --version` prints a one-line version number.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--short` | bool | `false` | Print the version number only |

## Sessions, plans, and usage

### `biu sessions`

View and export session logs. Logs live in `~/.biu/sessions/<project-dir>/<session-id>.jsonl`, bucketed by the directory biu was started in.

#### `biu sessions list`

Lists saved sessions per project (newest → oldest).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--all` | bool | `false` | List sessions of all projects, not just the current directory |

#### `biu sessions show <id>`

Prints all events of a session as raw JSONL. Argument: session id (required).

#### `biu sessions export <id>`

Exports a session to a human- or tool-friendly format. Redaction happens automatically before export (api_key / token / refresh_token / virtual_key field values plus Bearer and sk-ant-… patterns in free text).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-f, --format` | string | `markdown` | Output format: `markdown` (conversation transcript with tool-call boxes) \| `json` (structured export merged per turn) \| `anthropic-replay` (a message body you can POST directly to `/v1/messages`) |
| `--include-tool-output` | bool | `true` | Whether to render tool output; set to `false` when sharing sessions that touched sensitive files |
| `--exclude-system` | bool | `false` | Drop system_* events (permission denials, hook blocks, etc.) |
| `--max-tool-output-bytes` | int | `4096` | Truncate each tool output to N bytes (0 = no limit) |
| `-o, --output` | string | `""` | Write to a file instead of stdout |

### `biu plan`

View and manage plan files (the output of the `ExitPlanMode` tool), stored at `~/.biu/plans/<session-id>.md` (overridable with `BIU_PLANS_DIR`). `<ref>` accepts a full session id, an unambiguous prefix, or the literal `latest`.

| Subcommand | Description |
|------------|-------------|
| `biu plan list` | List plans (newest → oldest) with size and first-line preview |
| `biu plan show [<ref>\|latest]` | Print a plan (default latest) |
| `biu plan rm [<ref>]` | Delete a plan, or bulk-clean with `--older-than` |

Flags of `biu plan rm`:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--older-than` | string | `""` | Bulk-delete plans older than this duration (e.g. `30d`, `2w`, `4h`, `15m`, or any Go duration string) |

### `biu usage`

Summarizes the token usage records in `~/.biu/usage.jsonl`.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--since` | string | `7d` | Time window: `7d` / `30d` / `90d` / `all`, or an RFC3339 timestamp |
| `--bucket` | string | `day` | Grouping granularity: `day` \| `week` \| `month` |
| `--model` | string | `""` | Only count the given model id |
| `--json` | bool | `false` | Output JSON instead of a table |

```bash
biu usage --since 30d --bucket month
biu usage --model claude-opus-4-7 --json
```

## Configuration management

### `biu config`

Inspect and validate configuration files, output JSON Schemas, and manage the two toggles for telemetry and the update check.

| Subcommand | Description |
|------------|-------------|
| `biu config show` | Print the parsed config.toml (secret fields auto-masked) |
| `biu config validate` | Load all configuration layers and report problems per layer; any failing layer exits non-zero |
| `biu config schema [config\|settings]` | Output a JSON Schema document |
| `biu config telemetry …` | Manage optional telemetry (off by default) |
| `biu config update-check …` | Manage the startup update check (on by default) |

#### `biu config show`

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--settings` | bool | `false` | Show the merged settings.json layers (user / project / local) instead of config.toml |

#### `biu config validate`

Loads `~/.biu/config.toml` and every settings.json layer, printing ok / warn / fail per layer with the specific reason. No flags.

#### `biu config schema [config|settings]`

Takes one of the two arguments (required). Outputs the JSON Schema to stdout; pipe it to a file and reference it via `$schema` to get editor autocompletion and validation:

```bash
biu config schema settings > ~/.biumind/settings.schema.json
```

#### `biu config telemetry`

Telemetry is off by default. When enabled, anonymous events (subcommand name, outcome, duration, version, os/arch) are appended to `~/.biu/telemetry.jsonl` — never prompt contents, file paths, or API keys. The event file is directly inspectable for auditing.

| Subcommand | Description |
|------------|-------------|
| `biu config telemetry status` | Print current state, install_id, endpoint, and file path |
| `biu config telemetry on` | Enable (rotates the install_id) |
| `biu config telemetry off` | Disable (keeps the on-disk jsonl) |

Flags of `on`:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--endpoint` | string | `""` | Optional HTTPS reporting URL (if unset, only the local file is written) |

Environment variables: `BIU_TELEMETRY_DISABLED=1` (hard off), `BIU_TELEMETRY_ENABLED=1` (enable for one run), `BIU_TELEMETRY_ENDPOINT` (override the reporting URL).

#### `biu config update-check`

Checks for a new version when the interactive REPL starts (at most once every 24 hours, in the background, never blocking startup). headless, serve, and one-shot subcommands make no network request for this; development builds and desktop-client-managed builds always skip it. The environment variable `BIU_UPDATE_CHECK=0` turns it off for good.

| Subcommand | Description |
|------------|-------------|
| `biu config update-check status` | Print the toggle state, last check time, and the latest known version |
| `biu config update-check on` / `off` | Turn the startup update check on / off |

## MCP servers

### `biu mcp`

Diagnoses the MCP servers declared in the `[[mcp_servers]]` sections of `~/.biu/config.toml` and the tools they expose.

| Subcommand | Description |
|------------|-------------|
| `biu mcp list` | Launch all configured servers and list their tools (disabled ones are marked as skipped; launch failures show the specific error and missing environment variables) |
| `biu mcp probe <server-name>` | Launch a single server and print handshake info (name, protocol version, instructions) and the full tool catalog |

`probe` helps you tell apart whether the problem is in launching, the handshake, or a specific tool's schema. Neither subcommand has flags of its own; both time out after 30 seconds.

## Knowledge base ingestion

### `biu ingest <file>`

A one-shot pipeline: parse a local file (markdown / html / plain text), run a two-step chain-of-thought to generate a page draft (PageDraft), and print the result; with `--commit` the draft is pushed through the Wiki API to become a Wiki page (creating the page and its blocks).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--url` | string | `""` | Source URL (default `file://…`) |
| `--title` | string | `""` | Override the page title |
| `--json` | bool | `false` | Output the PageDraft as JSON |
| `--commit` | bool | `false` | POST the PageDraft to the Wiki API (requires `--project`) |
| `--project` | string | `""` | Project id or name (required with `--commit`) |
| `--wiki-url` | string | `""` | Override the Wiki API endpoint (default: derived from the model-relay endpoint) |
| `--wiki-token` | string | `""` | Override the Wiki API bearer token |

```bash
biu ingest README.md
biu ingest --json notes.md > page.json
biu ingest --commit --project Notes README.md
```

## Daemon and remote scheduling

How the three related commands fit together:

- `biu bridge`: a local IDE / local UI drives biu (biu opens a port and waits for connections).
- `biu agent worker`: biu actively connects to the server; jobs triggered remotely (phone / web) are dispatched to this machine for execution.
- `biu serve`: a superset of both (bridge HTTP + optional worker registration + PID file + health checks), mainly for desktop clients to launch as a subprocess; the first two commands remain as single-purpose entry points.

### `biu serve`

A long-running daemon. The typical use is the desktop client launching `biu serve --port 0 --pid-file ~/.biumind/biu.pid` and parsing `BIU_BRIDGE_URL=http://127.0.0.1:<port>` from stdout to learn the actual port; with `--register` it also outputs `BIU_DAEMON_ENV_ID=<env_id>` after successful registration. It exposes `GET /healthz` (liveness), `GET /metrics` (Prometheus), and `POST /internal/token` (loopback only, letting the client hot-swap the access token without a restart).

With `--pid-file` it watches its parent process: when the parent exits (e.g. the desktop app closes), the daemon shuts down gracefully, avoiding orphans; an old biu serve process pointed to by the PID file is safely taken over.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--port` | int | `0` | Listen port (0 = system-assigned; the actual port is in stdout's `BIU_BRIDGE_URL=…`) |
| `--listen` | string | `""` | Explicit listen address (overrides `--port`, e.g. `0.0.0.0:8088`) |
| `--auth-token` | string | `""` | Bearer token required on every request (empty = no authentication, development only) |
| `--pid-file` | string | `""` | PID file path; a live existing process blocks startup, stale files are cleaned automatically |
| `--register` | bool | `false` | Also register as an Agent Plane environment (biu_daemon worker); remote clients can then schedule this machine |
| `--brain-url` | string | `""` | brain service URL (default: `BIUMIND_BRAIN_URL` or `[relay].endpoint`) |
| `--identity-url` | string | `""` | identity service URL, used for client-side BYOK key retrieval (default: `BIUMIND_IDENTITY_URL` or brain-url) |
| `--allowed-roots` | string[] | — | Filesystem roots this daemon may touch; repeatable; empty = the daemon's current directory only |
| `--tool-policy` | string | `workspace-write` | Capability floor: `readonly` \| `workspace-write` \| `full` |

```bash
# Run manually with registration
BIUMIND_PAT=<pat> biu serve --register --brain-url https://your-biumind.example.com
```

> [!TIP]
> `--tool-policy` is an independent local hard floor — the daemon does not blindly trust the server: the effective policy is the intersection of the local flag and the per-device policy pushed by the server (the local flag is the upper bound; the server can only narrow it). `readonly` disables all dangerous tools; `workspace-write` allows file reads and writes (still constrained by the `--allowed-roots` paths) but disables the shell / subagents; `full` sets no capability floor. Out-of-bounds paths are denied outright, never prompted.

### `biu bridge`

Exposes the agent over HTTP/SSE to an IDE or remote UI. Every request builds a fresh agent instance; state is not shared across clients. At startup it runs a configuration smoke test first, so a broken config fails immediately instead of at the first request.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--listen` | string | `:8088` | Listen address (with `:0`, the actual port is printed to stderr) |
| `--auth-token` | string | `""` | Bearer token required on every request (empty = no authentication, development only) |

### `biu agent worker`

Registers this machine as an Agent Plane execution environment (type `biu_daemon`), long-polls for jobs, runs agents locally, and pushes streaming frames back to the server for remote clients to watch. Credential resolution priority: device token (from `biu pair`) > `BIUMIND_PAT` > `--token` > `BIUMIND_TOKEN` > `virtual_key` from the config.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--brain-url` | string | `""` | brain service URL (default: `BIUMIND_BRAIN_URL` or `[relay].endpoint`) |
| `--machine-name` | string | `""` | Machine name reported to the server (default: hostname) |
| `--pool-tag` | string | `""` | Optional pool tag for runtime-style routing |
| `--allowed-roots` | string[] | — | Filesystem roots this worker may touch; repeatable; empty = the startup directory only |
| `--tool-policy` | string | `workspace-write` | Capability floor: `readonly` \| `workspace-write` \| `full` (intersected with the server-side policy) |

```bash
biu pair                 # pair first to get a device token
biu agent worker --brain-url https://your-biumind.example.com
```

## Plugins and skills

### `biu plugin`

Local plugin management. A plugin is a directory that can bundle commands, subagents, skills, output styles, hooks, and MCP servers. All write operations land in the user layer (`~/.biumind/plugins/`, `~/.biumind/settings.json`); project-layer / local-layer files are never touched.

| Subcommand | Description |
|------------|-------------|
| `biu plugin list` | List all discovered plugins (user + project + compatibility directories), with enabled state and a component summary |
| `biu plugin show <name>` | Print a plugin's full details |
| `biu plugin install <path\|plugin@marketplace>` | Install from a local directory or a registered marketplace |
| `biu plugin uninstall <name>` | Remove from `~/.biumind/plugins/` |
| `biu plugin enable <name>` / `disable <name>` | Enable / disable (rewrites the disable list in `~/.biumind/settings.json`; takes effect on restart) |
| `biu plugin validate <path>` | Validate a plugin directory's manifest and component layout (aimed at plugin authors) |
| `biu plugin marketplace …` | Marketplace management (alias `market`) |

#### `biu plugin install`

Three installation forms:

```bash
biu plugin install ./my-plugin            # local directory (copied into ~/.biumind/plugins/<name>/)
biu plugin install /abs/path/to/plugin    # absolute path
biu plugin install code-review@biumind    # install from a registered marketplace
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--force` | bool | `false` | Overwrite an existing installation of the same name |

#### `biu plugin marketplace`

| Subcommand | Description |
|------------|-------------|
| `biu plugin marketplace list` | List registered marketplaces (name, whether signed, source) |
| `biu plugin marketplace add <name> <source>` | Register a marketplace. Sources support local paths / `https://…` JSON URLs / `git+https://…` |
| `biu plugin marketplace remove <name>` | Unregister a marketplace (alias `rm`) |
| `biu plugin marketplace show <name>` | Fetch and list the marketplace's plugins, with install-command hints |

Flags of `add`:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--pinned-key` | string | `""` | A pinned ed25519 public key used to verify the marketplace.json signature (format `ed25519:<base64-spki>`; the key pair can be generated with `biu skill keygen`). If unset, no integrity verification is performed |

```bash
biu plugin marketplace add biumind-official git+https://github.com/biumind/marketplace
biu plugin marketplace show biumind-official
biu plugin install code-review@biumind-official
```

### `biu skill`

Skill (SKILL.md) management: local/cloud sync, installation, packaging, and signing. `list / pull / push / diff / enable / disable / install` need a runtime URL — pass `--runtime-url` or set `BIUMIND_RUNTIME_URL`; `run / pack / unpack / keygen / sign / verify` work fully offline.

| Persistent flag | Type | Default | Description |
|------|------|---------|-------------|
| `--runtime-url` | string | `""` | Runtime endpoint (overrides `BIUMIND_RUNTIME_URL`) |

| Subcommand | Description |
|------------|-------------|
| `biu skill list` | List cloud skills |
| `biu skill pull` | Sync cloud skills to `~/.biumind/skills/` |
| `biu skill push <identifier>` | Upload a local SKILL.md to the cloud (create or update) |
| `biu skill diff <identifier>` | Compare local and cloud hashes |
| `biu skill run <identifier> [args…]` | Offline-expand a local SKILL.md (`$ARGS` substitution) to stdout; no model call |
| `biu skill install <url\|path>` | Install from an HTTPS SKILL.md URL or a local `.biuskill` package |
| `biu skill pack <dir>` / `unpack <file.biuskill>` | Pack / unpack a `.biuskill` archive |
| `biu skill keygen` / `sign` / `verify` | ed25519 key pair generation and `.biuskill` signing / verification |
| `biu skill enable <skill-id>` / `disable <skill-id>` | Turn a skill on / off on a specific agent |

Flags of each subcommand:

**`biu skill list`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--status` | string | `""` | Filter by status: `active` / `disabled` / `staged` / `staged_org` / `suspended` |
| `--source` | string | `""` | Filter by source: `bundled` / `org` / `user` / `marketplace` / `imported` |

**`biu skill install <url|path-to-biuskill>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--agent` | string | `""` | After installing, also enable the skill on the given agent (UUID) |
| `--pin` | bool | `false` | With `--agent`: pin the skill so its body is always inlined into the system prompt |
| `--dry-run` | bool | `false` | Only resolve the source (URL rewrite or local unpack) and print what would be installed, without contacting the server |

> [!NOTE]
> When `pull` hits a local-vs-cloud content conflict (diverged), it exits non-zero and suggests checking first with `biu skill diff <name>`, then either `biu skill push <name>` to upload the local version, or delete the local file to accept the cloud's.

**`biu skill pack <dir>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-o, --output` | string | `""` | Output path (default `<dir>.biuskill`) |

Packing is deterministic (mtime / order / mode are fixed): packing the same source bytes twice yields identical output — this is the precondition for ed25519 signing. Only SKILL.md and the `scripts/` / `references/` / `assets/` directories are included; other files are skipped with a notice.

**`biu skill unpack <file.biuskill>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-o, --output` | string | `""` | Output directory (default: the path with the `.biuskill` extension removed) |

**`biu skill keygen`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--prefix` | string | `""` | Output filename prefix (default `biuskill`, producing the `biuskill.key` private key with 0600 and the `biuskill.key.pub` public key). Refuses to overwrite an existing private key |

**`biu skill sign <pack.biuskill>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--key` | string | `""` | Path to a PEM-encoded ed25519 private key (required). The signature is written to `<pack>.sig` |

**`biu skill verify <pack.biuskill>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--pubkey` | string | `""` | Path to the publisher's PEM public key (required) |
| `--sig` | string | `""` | Signature path (default `<pack>.sig`) |

**`biu skill enable / disable <skill-id>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--agent` | string | `""` | Target agent UUID (required) |
| `--pin` | bool | `false` | enable only: pin the skill body inline into the system prompt |

## App development and local running

### `biu app`

The toolchain for developing, packaging, and inspecting App Center BiuApps.

| Subcommand | Description |
|------------|-------------|
| `biu app new <slug>` | Scaffold a new App project from a built-in template into `<slug>/` |
| `biu app validate` | Validate manifest.yaml (lists all problems at once rather than only the first) |
| `biu app inspect` | Print the parsed manifest (permissions, data scopes, actions, views, triggers, sidebar config) |
| `biu app pack` | Package a `.biuapp` distribution (manifest validation is enforced before packing) |
| `biu app verify <file.biuapp>` | Verify a distribution's hash and signature |
| `biu app keygen` | Generate an ed25519 publisher key pair |
| `biu app run` | Local development server |

Flags of each subcommand:

**`biu app new <slug>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--from` | string | `hybrid_full` | Template name: `minimal` \| `view_only` \| `hybrid_full` |

**`biu app validate` / `biu app inspect`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--manifest` | string | `""` | Path to manifest.yaml (default `./manifest.yaml`) |
| `--json` (inspect only) | bool | `false` | Output the parsed result as JSON |

**`biu app pack`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--source` | string | `""` | Project root (default: current directory) |
| `--out` | string | `""` | Output path (default `dist/<slug>-<version>.biuapp`) |
| `--key` | string | `""` | Signing private key path (default `~/.biumind/keys/publisher.ed25519`) |
| `--unsigned` | bool | `false` | Skip signing (local installation only; marketplace submissions are rejected) |

The packing scope is determined by `.biuapp.yaml` (the include list) at the project root; without that file it falls back to `manifest.yaml + README.md + LICENSE`.

**`biu app verify <file.biuapp>`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--trust-key` | string[] | — | Trusted publisher key pairs (private key paths); repeatable |

**`biu app keygen`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--name` | string | `publisher` | Key name (files are `<name>.ed25519`); the output includes the publisher id to write into manifest.yaml |

**`biu app run`**

The local development server. On startup it validates the manifest, binds the dev port, launches `go run` subprocesses as needed, and watches for file changes (manifest and Go source edits auto-reload / restart the subprocess). The desktop client discovers the App in the "In development" panel. While running, press `r` to restart the subprocess manually, `q` to quit.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--dev` | bool | `false` | Development mode (required in the current version) |
| `--source` | string | `""` | App source directory (default: current directory) |
| `--addr` | string | `127.0.0.1:7099` | Dev server listen address |
| `--mock` | string | `""` | Fixtures directory: calls return mock data from `<action>.json` instead of the subprocess (setting this flag skips the subprocess automatically) |
| `--no-subproc` | bool | `false` | Skip the `go run` subprocess (view-only Apps) |

### `biu repo-app`

Clones a GitHub open-source project locally, auto-detects the tech stack, installs dependencies, and runs it as a local web service (127.0.0.1 only). Instances live in `~/.biumind/repo-apps/` (overridable with `BIU_REPOAPP_ROOT` or the `[repo-app].cache_dir` config). Currently macOS / Linux only.

Once the health check passes, `ensure` / `run` print `BIU_REPOAPP_URL=http://127.0.0.1:<port>` to stdout (the same convention as `biu serve`'s `BIU_BRIDGE_URL`) for the desktop client to parse.

| Subcommand | Description |
|------------|-------------|
| `biu repo-app install <github-url\|owner/repo>` | Clone, detect the stack, install dependencies |
| `biu repo-app ensure <name\|github-url\|owner/repo>` | Idempotent bring-up: install if missing, start if stopped, reuse if running |
| `biu repo-app list` | List installed instances and their running state |
| `biu repo-app run <name>` | Start as a detached local service (127.0.0.1 only) |
| `biu repo-app stop <name>` | Stop (SIGTERM, then SIGKILL after 3 seconds) |
| `biu repo-app logs <name>` | View run logs |
| `biu repo-app update <name>` | Pull the new ref, reinstall dependencies, restart |
| `biu repo-app remove <name>` | Stop and delete the instance directory |
| `biu repo-app doctor` | Probe the local runtimes (git / python3 / uv / node / mise / docker) and report |

Flags of each subcommand:

**`biu repo-app install`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--ref` | string | `""` | Branch / tag to install (default: the repository's default branch) |

**`biu repo-app ensure` / `biu repo-app run`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--env` | string[] | — | `KEY=VALUE` merged into the instance's `.env` (0600); repeatable; flag values override existing ones |
| `--port` (run only) | int | `0` | Port bound to 127.0.0.1 (0 = system-assigned) |

**`biu repo-app logs`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-f, --follow` | bool | `false` | Keep following the log output |

**`biu repo-app update`**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--ref` | string | `""` | Branch / tag to switch to (default: the installed ref) |
| `--install-id` | string | `""` | App Center install id; together with `--build-id`, reports the update result back to the server |
| `--build-id` | string | `""` | App Center build id (from a redeploy call); pairs with `--install-id` |
| `--report-url` | string | `""` | Base URL for build reporting (default `[model-relay].endpoint`) |

```bash
biu repo-app install owner/repo
biu repo-app run owner-repo --port 8123
biu repo-app logs owner-repo -f
```

> [!NOTE]
> Missing runtimes detected by `biu repo-app doctor` are auto-installed by `install` / `update` (e.g. uv / mise); but if the project ships a Dockerfile and Docker is missing, it fails outright and asks you to install Docker first.

## Environment variables

| Variable | Purpose |
|----------|---------|
| `BIU_CONFIG` | Override the config.toml path |
| `BIUMIND_MODEL_RELAY_URL` | Override the model-relay endpoint (lower priority than `--model-relay-url`) |
| `BIUMIND_TOKEN` | Override the bearer token (lower priority than `--token`) |
| `BIUMIND_PAT` | Personal access token for the Agent Plane worker |
| `BIUMIND_DEVICE_TOKEN` | Device token (product of `biu pair`; takes precedence over the PAT) |
| `BIUMIND_BRAIN_URL` | brain service URL |
| `BIUMIND_IDENTITY_URL` | identity service URL |
| `BIUMIND_RUNTIME_URL` | Runtime endpoint (`biu skill` cloud operations) |
| `BIUMIND_LOG_LEVEL` | `biu serve` log level (`debug` raises it to debug) |
| `BIU_TELEMETRY_DISABLED` / `BIU_TELEMETRY_ENABLED` / `BIU_TELEMETRY_ENDPOINT` | Telemetry hard off / one-run enable / reporting URL override |
| `BIU_UPDATE_CHECK` | `0` = hard-disable the startup update check |
| `BIU_PLANS_DIR` | Override the plan files directory |
| `BIU_REPOAPP_ROOT` | Override the repo-app instances root |

In cloud mode the token resolution priority is: `--token` > `BIUMIND_TOKEN` > the config's `[model-relay].virtual_key` > OAuth storage (the token from `biu auth login`, auto-refreshable). `biu doctor` shows which source is actually in effect.
