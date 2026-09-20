# Quick Start

`biu` is BiuMind's terminal AI coding agent: a ~15 MB static Go binary with no runtime dependencies. Talk to a model in your terminal and let it read and write code, run commands, and manage Git — all under the control of the permissions system and the sandbox.

This guide walks you from zero to your first working agent session: install → initial configuration → login → self-check. For day-to-day usage see [usage.md](usage.md); for a reference of all subcommands see [commands.md](commands.md).

## Installation

### Homebrew (recommended, macOS / Linux)

```sh
brew install biumind/tap/biu
biu version
```

### Prebuilt binaries (darwin / linux × amd64 / arm64)

Download the archive for your platform from [GitHub Releases](https://github.com/biumind/biumind/releases) (file names look like `biu_0.2.0_Darwin_arm64.tar.gz`), verify, and install:

```sh
tar -xzf biu_*_$(uname -s)_$(uname -m).tar.gz
install -m 0755 biu /usr/local/bin/biu
biu doctor
```

> [!TIP]
> Every release ships with `checksums.txt` (SHA256 for all artifacts). Run `sha256sum -c checksums.txt` after downloading to verify integrity.

### Build from source (Go 1.25+)

```sh
go install github.com/biumind/biumind/apps/cli/biu/cmd/biu@latest
```

Or clone the repository and build locally:

```sh
git clone https://github.com/biumind/biumind.git
cd biumind/apps/cli/biu
go build -o biu ./cmd/biu
```

> [!NOTE]
> If you already have biu installed, you can self-update from the REPL with `/upgrade run` (installs done via brew / go install are delegated to the corresponding package manager). Use `/install` to see how biu was installed along with version information.

## Three deployment modes

`biu` uses `[default].mode` in `~/.biu/config.toml` to decide which path model calls take:

| Mode | When to use | What you need |
|------|-------------|---------------|
| `cloud` (default) | Calls go through the BiuMind model-relay gateway; quotas and billing are handled server-side | model-relay URL + login token |
| `direct` | Personal use with your own Anthropic API key, connecting directly | `[providers.anthropic].api_key` |
| `byo_endpoint` | Self-hosted model-relay / proxy | Your own model-relay URL + token |

> [!WARNING]
> The full agent loop (tool calls, permissions, hooks) is currently available only in `cloud` and `direct` modes. `byo_endpoint` does not support the engine path; once configured it can only be used for plain conversation.

## First-time configuration: `biu init`

`biu init` is an interactive wizard: choose a deployment mode → enter credentials → write `~/.biu/config.toml`. If a configuration already exists, it asks before overwriting — nothing is destroyed silently.

```sh
biu init
```

At the end, the wizard automatically runs a connectivity smoke test (`direct` mode probes `https://api.anthropic.com`; other modes call the model-relay's `/healthz`), so you catch typos right away instead of during your first conversation.

Every interactive prompt can be replaced with a flag, which is convenient for scripting / CI:

```sh
# direct mode in a single command
biu init --yes --mode direct --api-key sk-ant-xxxx --model claude-sonnet-4-6

# cloud mode: log in first (the token goes into the OS keychain), then write the config
biu auth login
biu init --yes --mode cloud --model-relay-url https://biumind.xxlab.tech --model <model-id>
```

All flags of `biu init`:

| Flag | Description |
|------|-------------|
| `--mode` | `cloud` \| `byo_endpoint` \| `direct`; skips the mode selection |
| `--api-key` | Anthropic API key (used with `--mode=direct`) |
| `--model-relay-url` | model-relay endpoint (used with `cloud` / `byo_endpoint`) |
| `--model-relay-token` | model-relay token (when not going through browser login) |
| `--model` | Default model; skips the model prompt |
| `--with-memory` | Also generate a `BIUMIND.md` template in the current directory (see [biumind-md.md](biumind-md.md)) |
| `--with-settings` | Also generate an initial `~/.biumind/settings.json` permissions configuration (see [permissions.md](permissions.md)) |
| `--yes` | Skip all interactive prompts and rely entirely on flags |

### `~/.biu/config.toml` structure

Config file lookup order: `--config <path>` flag → `BIU_CONFIG` environment variable → `~/.biu/config.toml` → built-in defaults. The file permission should be `0600` (`biu doctor` checks this).

A complete example (every section is optional — fill in what you need):

```toml
[default]
mode     = "cloud"       # cloud (default) | byo_endpoint | direct
provider = "anthropic"   # in direct mode, refers to the [providers.<name>] section
model    = "claude-sonnet-4-6"

[model-relay]
endpoint    = "https://biumind.xxlab.tech"
virtual_key = "..."      # Not needed for browser-login users (the token lives in the keychain)

[providers.anthropic]    # used in direct mode
api_key   = "sk-ant-..."
endpoint  = ""           # empty means https://api.anthropic.com

[permissions]            # legacy permissions config; settings.json is recommended now
mode = "ask"             # ask | auto_edit | full_access (legacy vocabulary, equivalent to default / acceptEdits / bypassPermissions)

[search]                 # how the websearch tool resolves queries
mode        = "model-relay"  # model-relay (default) | direct
searxng_url = ""             # required when mode=direct

[auth]                   # OAuth overrides; usually unnecessary — endpoints are derived from [model-relay].endpoint
authorize_url   = ""
token_url       = ""
revoke_url      = ""
client_id       = ""
scopes          = []
callback_port   = 0      # 0 = pick a free port automatically
manual_redirect = ""

[[mcp_servers]]          # MCP servers, see usage.md
name    = "filesystem"
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
```

Field reference (one-to-one with the parsing code, see `apps/cli/biu/internal/config/config.go`):

- **`[default]`**: `mode` is the deployment mode; `provider` is the provider name for direct connections (default `anthropic`); `model` is the default model — **the default value is empty and there is no built-in fallback**; if unset, startup fails with an error guiding you to configure it.
- **`[model-relay]`**: `endpoint` is the gateway URL (built-in default `https://biumind.xxlab.tech`); `virtual_key` is a static token, not needed for browser-login users.
- **`[providers.<name>]`**: `api_key` plus an optional `endpoint`. Providers other than `anthropic` connect via the OpenAI-compatible protocol.
- **`[permissions]`**: the legacy permissions section (`mode` + `allowlist`), plus `plan_drift_threshold`, `suggest_plan_for`, and `suggest_plan_disabled` controlling plan-mode suggestions. Rules are best written in `settings.json` (see [permissions.md](permissions.md)).
- **`[search]`**: whether the `websearch` tool goes through model-relay or your own SearxNG instance.
- **`[auth]`**: OAuth endpoint overrides. By default, `/oauth/authorize`, `/oauth/token`, and `/oauth/revoke` are derived from the `scheme://host` of `[model-relay].endpoint`; the client ID is fixed to the pre-registered public client `biu-cli`.
- **`[[mcp_servers]]`**: an array of MCP servers, supporting both stdio and HTTP transports; see [usage.md](usage.md#mcp-servers).

> [!TIP]
> `biu config show` prints the currently effective merged configuration; `biu config validate` loads all configuration layers and reports problems; `biu config schema config` / `biu config schema settings` output the JSON Schema for the corresponding file, which is handy for editor autocompletion.

## Login: `biu auth`

In cloud / byo_endpoint mode, OAuth authorization (PKCE, no client secret) is completed in the browser:

```sh
biu auth login
```

Flow: `biu` starts a one-time callback listener on `127.0.0.1:<free port>` → opens (or prints) the authorization URL → you confirm in the browser → the callback delivers the authorization code → tokens are exchanged and saved. Default timeout is 5 minutes.

For SSH / headless environments, use the manual paste mode:

```sh
biu auth login --manual
```

The terminal prints an authorization URL; open it in a browser on any machine, and after authorizing, paste the full redirect URL back into the terminal (`biu` extracts `code` and `state` from it and validates state against CSRF).

### Where the token is stored

`biu` prefers the OS keychain and falls back to a file when unavailable:

- **macOS**: Keychain (service name `com.biumind.biu`)
- **Linux**: requires `secret-tool` (the `libsecret-tools` package) to be available, accessing the system credential store via D-Bus
- **Other platforms**: no keychain backend yet; the file fallback is used directly
- **Fallback**: `~/.biu/auth.json` (permission `0600`)

`biu auth status` shows the current backend, a masked token summary, scopes, and expiry; `biu auth logout` first revokes the refresh token server-side (a failure only produces a warning — the local logout always happens) and then deletes the local token; users of older versions can run `biu auth migrate` to move tokens from `~/.biu/auth.json` into the keychain in one shot.

> [!NOTE]
> Token resolution priority for API calls: `--token` flag > `BIUMIND_TOKEN` environment variable > `[model-relay].virtual_key` from the config > OAuth storage (keychain / file). The `auth token source` check in `biu doctor` shows which source is actually in effect.

## Self-check: `biu doctor`

```sh
biu doctor
```

It runs each check and prints a colorized list: `✓` OK, `!` degraded but usable, `✗` failed (any failure exits with a non-zero code). Checks include:

| Check | What it covers |
|-------|----------------|
| `config` / `config perm` | Config parses; file permission is `0600` (there are API keys inside) |
| `mode` / `model` / `perm mode` | Current deployment mode, default model, permissions mode |
| `provider` / `endpoint` / `api key` | direct mode: key presence (masked display) and endpoint |
| `connectivity` | direct mode: probes `<endpoint>/v1/messages` (<500 counts as online) |
| `model-relay URL` / `model-relay healthz` | relay modes: URL and `/healthz` liveness probe |
| `~/.biu` / `~/.biumind` | Directory existence and permissions (must not be group / world writable) |
| `git` / `rg` / `gopls` | External tools on PATH (missing only causes a degraded warning) |
| `sandbox-exec` (macOS) / `bwrap` (Linux) | Sandbox tooling availability; if missing, Bash runs unsandboxed |
| `settings.*` / `sandbox` | Loaded settings layers; merged sandbox rule counts (guards against silently ineffective JSON key typos) |
| `auth backend` / `oauth token` | Token storage backend; access-token expiry state and refresh capability |
| `auth token source` | The token source currently in effect (see the priority order above) |
| `update check` | The startup update check's toggle and state (reads local state, no network) |
| `agent-plane secrets` | Storage backend for remote-scheduling credentials (device token / private key) |

> [!TIP]
> When you hit "I logged in but still get 401", look at the `oauth token` line first: `expired — will refresh on next API call` means it will auto-refresh on the next call; `NO refresh_token` means you need to run `biu auth login` again.

## Your first session

```sh
cd ~/code/your-project
biu
```

On startup the REPL prints the current mode / provider / model. Type natural language directly to start a conversation; type `/` to open the slash-command palette. Common keys: `Ctrl-C` interrupts streaming output (press again while idle to quit), `Ctrl-D` quits.

```text
> What does this project do? Start by reading the README and a few top-level source files.
```

Next steps:

- [usage.md](usage.md) — REPL, slash commands, MCP, permission prompts, cost tracking, session management, IDE integration (`biu bridge` / `biu serve`)
- [permissions.md](permissions.md) — permission rule syntax, modes, and hooks
- [biumind-md.md](biumind-md.md) — the `BIUMIND.md` memory file format
- [sandbox.md](sandbox.md) — the Bash sandbox policy
