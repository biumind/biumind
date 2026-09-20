# Usage Guide

This page covers the day-to-day use of `biu`: the REPL, slash commands, MCP, permission prompts, cost tracking, session management, and IDE / desktop integration (`biu bridge` and `biu serve`). For installation and first-time setup see [getting-started.md](getting-started.md); for a full reference of all subcommands and flags (including `biu agent`, `biu pair`, `biu plan`, `biu skill`, and others not expanded here) see [commands.md](commands.md).

## REPL basics

Run it directly inside a project directory:

```sh
biu
```

On startup it prints a one-line status to stderr (`[biu] mode=… provider=… model=…`), then enters the interactive UI:

- Type natural language to send a message; the **working directory is persistent** (kept across commands), but shell state (environment variables, `cd`) is not.
- Type `/` to open the slash-command palette: keep typing to filter by prefix, `↑` / `↓` to move, `Tab` to complete, `Enter` to select and run.
- `Ctrl-C`: while streaming = interrupt the current turn and keep what was generated; while idle = quit.
- `Ctrl-D`: quit.
- The status bar (bottom) shows in real time: the model, the permissions-mode badge (in non-default modes the whole status bar is tinted by mode), accumulated spend (`$x.xxxx`), context usage (`ctx NN% [██░░…]`), turn / streaming status, and the output of your custom status-line script.

Common top-level flags (also effective on subcommands):

```sh
biu --model <id>                # temporarily switch model
biu --continue                  # resume the most recent session of the current project
biu --resume <session-id>       # resume a specific session (the transcript is replayed into the engine; the original file is kept)
biu --no-log                    # skip session logging for this run
biu --add-dir ../docs           # temporarily add an extra read-write working directory
```

## Slash commands

Type `/help` in the REPL at any time to see the command list. The full list follows (`[]` marks optional arguments):

### Memory and initialization

| Command | Description |
|---------|-------------|
| `/init [--force\|--dry-run]` | Scan the current project and generate a starter `BIUMIND.md` (pre-filled with build / test / lint commands) |
| `/memory [list\|reload]` | Show loaded memory files and auto-memory status; `reload` re-reads after edits and hot-updates the system prompt |
| `/remember [-t <type>] <text>` | Save a memory to `~/.biumind/memory` (default type=user) |

See [biumind-md.md](biumind-md.md) for the memory file format and loading hierarchy.

### Sessions and history

| Command | Description |
|---------|-------------|
| `/sessions` | List recent sessions |
| `/resume [#n\|latest\|<id>]` | Resume a past session; without arguments, opens a numbered picker |
| `/rename [<title>\|clear]` | Name the current session so it's easy to spot in `/resume` and `/sessions` |
| `/export <path> [--format md\|json\|anthropic-replay]` | Export the current session to a file |
| `/share [md\|json\|<path>]` | Export the session to a temp file and copy the path to the clipboard |
| `/rewind [<uuid> [--dry-run]]` | List captured file snapshots; restore files to their state before a given message |
| `/clear` | Clear history and start over |
| `/compact` | Summarize older conversation to free context window |
| `/stats` | Current session statistics: duration, message count, tokens, files |
| `/summary` | A structured summary of this session (no model call) |

### Permissions and security

| Command | Description |
|---------|-------------|
| `/permissions` | Show the permission rules and mode currently in effect (read-only) |
| `/mode <mode>` | Switch permissions mode (`default` / `acceptEdits` / `plan` / `bypass`) |
| `/add-dir <path> [--remember]` | Register an extra working directory; `--remember` persists it to `.biumind/settings.local.json` |
| `/remove-dir <path>` | Remove a working directory |
| `/hooks [<event-substring>]` | List hooks registered on each event, filterable by event name |
| `/trust [here\|session\|add <p>\|remove <p>]` | Manage which directories are trusted to run shell hooks / status-line scripts |
| `/reload` | Force-reload settings.json (permissions take effect immediately; hooks need a restart) |

See [permissions.md](permissions.md) for permission rule syntax and hooks configuration.

### Model and output

| Command | Description |
|---------|-------------|
| `/model <id>` | Switch the model for this session |
| `/effort [high\|medium\|low\|<model-id>]` | Switch the reasoning-effort tier; without arguments, shows the current tier |
| `/fast` | Shortcut for `/effort low`, switching to the fastest, cheapest model |
| `/output-style <name>` | Switch output style (concise / explanatory / …) |
| `/theme [dark\|light\|system]` | Switch the color scheme |
| `/break-cache` | Force the next request to skip the prompt cache (debugging only) |

### Cost tracking

| Command | Description |
|---------|-------------|
| `/cost [--by-tool]` | Accumulated tokens and dollar spend for this session; `--by-tool` lists per-tool call counts / durations / output bytes / errors |
| `/usage [today\|week\|month\|all] [<model-prefix>]` | Accumulated tokens and dollar spend from the historical usage ledger, filterable by model prefix |

The command-line equivalent is `biu usage` (supports `--since 7d|30d|all`, `--bucket day|week|month`, `--model <id>`, `--json`).

### Git and GitHub

| Command | Description |
|---------|-------------|
| `/commit [--dry-run\|--no-stage\|-m "msg"]` | Stage + have the model draft a Conventional Commits message + commit |
| `/pr [--dry-run\|--no-push\|--draft\|--base <br>\|--title <t>]` | Push the branch + model-drafted PR title / body + create it via `gh` |
| `/issue [<n>\|comment <n> "x"\|close <n>]` | List / view / comment on / close GitHub issues |
| `/pr-comments [<n>]` | View PR review comments (uses the current branch when the number is omitted) |
| `/branch` | Current branch, upstream, dirty state, recent commits |
| `/diff [staged\|<ref>]` | Compact `git diff --stat` output |
| `/tag [<name> [-m "msg"\|--auto [--from <prev>]]]` | List / create tags; `--auto` has the model draft the changelog |

### Subagents and planning

| Command | Description |
|---------|-------------|
| `/agents [create <name> …]` | List registered subagent types, or scaffold a new one |
| `/todo` | Print the in-session task list |
| `/plan [list\|show <id>\|diff\|approvals]` | List / print plans, diff a plan against actual tool calls, audit batch approvals |
| `/ultraplan <task>` | Dispatch a Plan subagent to design an implementation |
| `/review [scope]` | Dispatch a CodeReview subagent (reviews the current branch diff by default) |
| `/verify [scope]` | Dispatch a Verification subagent that actually runs the changes and ends with VERDICT: PASS/FAIL/PARTIAL |
| `/workflow [<name> [args]\|show <name>]` | List / preview / trigger custom multi-step workflows |

### Tools and runtime environment

| Command | Description |
|---------|-------------|
| `/mcp [<server>]` | List connected MCP servers and their tools; given a name, drill into a single server |
| `/tasks [list\|output <id> [n]\|kill <id>\|killall]` | Manage background Bash tasks |
| `/plugin [<name>\|enable <n>\|disable <n>\|reload]` | Plugin management; enable / disable is persisted to settings.json |
| `/doctor` | In-REPL health check (runtime / git / shell / engine / MCP) |
| `/env [<filter>]` | Show biu-related environment variables (KEY/TOKEN/SECRET auto-masked) |
| `/ide` | Show the IDE bridge endpoint and hookup instructions |

### Account, updates, and misc

| Command | Description |
|---------|-------------|
| `/login` | Show OAuth token status (logged in or not, expiry time) |
| `/logout` | Delete the local OAuth token (does not revoke it server-side) |
| `/upgrade [run\|check\|check skip]` | Update biu: manual installs self-update; brew / go install / snap are delegated to the package manager |
| `/install` | Binary diagnostics: version, commit, install method, update command |
| `/release-notes [full\|<substring>]` | Show biu release notes (last 80 lines by default) |
| `/telemetry [tail [N]\|export <path>\|enable <endpoint>\|disable]` | Telemetry status, log tail / export (off by default) |
| `/feedback ["summary"\|--print]` | Open a GitHub issue pre-filled with version and session diagnostics |
| `/onboarding` | Beginner onboarding |
| `/copy [code\|<pattern>]` | Copy the latest assistant reply (or a code block / matching snippet in it) to the clipboard |
| `/help` | Show the command list |
| `/quit` | Quit |

> [!TIP]
> The `~/.biumind/commands/` directory lets you define custom slash commands in Markdown; they appear in the `/` palette alongside the built-in commands.

## Permission prompts

When a tool call is not allowed by any rule and the current mode requires confirmation, the REPL shows an inline confirmation box above the input field with the tool name, an input summary, and the reason, plus shortcuts:

- `a` — allow once
- `shift+a` (or `s`) — allow and remember (same-kind calls stop asking for the rest of the session)
- `d` — deny
- `q` / `esc` — deny and interrupt the current turn

Some prompts carry a precomputed suggestion shortcut (e.g. `w` = "allow and add this directory as a working directory"); pressing it applies the adjustment and allows the call.

See [permissions.md](permissions.md) for the full details of rules, modes, and the decision order.

## Cost tracking

Three levels of view:

1. **Status bar**: real-time accumulated dollar spend (hidden below $0.0001) and a context-usage bar.
2. **`/cost`**: the full bill of the current session — input / cache read / cache write / output tokens, cache hit rate, dollar total, and last-turn context usage; `/cost --by-tool` adds a leaderboard of per-tool call counts, durations, output bytes, and errors.
3. **`/usage` and `biu usage`**: the cross-session historical ledger, backed by `~/.biu/usage.jsonl` (a record is appended automatically after each turn).

## Session management

Session logs are stored as JSONL under `~/.biu/sessions/<project-dir>/<session-id>.jsonl` — bucketed by the directory biu was started in, so `--continue` only finds the most recent session of the current project. `--no-log` disables logging for a single run.

```sh
biu sessions list           # sessions of the current project, newest first
biu sessions list --all     # all projects
biu sessions show <id>      # print all events of a session (JSONL)
biu sessions export <id> --format markdown   # export as markdown / json / anthropic-replay
```

> [!TIP]
> `biu sessions export` automatically redacts before exporting (api_key / token / refresh_token / virtual_key fields, plus Bearer and sk-ant-… patterns in free text); `--include-tool-output=false` drops all tool output, which is useful when sharing sessions that touched sensitive files.

Resume semantics:

- `biu --resume <id>` / `biu --continue`: replays the session's event log into the engine and continues the conversation. **Resuming always lands on a new session id**; the original log file is never modified (the `--fork-session` flag is therefore an explicit no-op, kept only for compatibility with old habits).
- If the original session ran inside a worktree, resuming also returns you to that worktree directory and branch.
- `biu --rewind-files <uuid> --resume <id>`: restores the file system to its state before a given user message, then exits (pairs with `/rewind` in the REPL; the message UUID comes from the `user_message` event in the session JSONL).

The REPL counterparts are `/sessions`, `/resume`, `/rename`, `/export`.

## MCP servers

Declare MCP servers in `~/.biu/config.toml`; `biu` launches them at startup and merges their tools into the local tool catalog, named `mcp__<server-name>__<tool-name>`.

### stdio transport (local subprocess)

```toml
[[mcp_servers]]
name    = "filesystem"
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
env     = { GITHUB_TOKEN = "ghp_…" }   # optional
cwd     = ""                            # optional
```

### HTTP transport (Streamable HTTP)

```toml
[[mcp_servers]]
name      = "github"
transport = "http"
url       = "https://example.com/mcp/"
headers   = { Authorization = "Bearer …" }
```

If an HTTP server responds with 401 + a Bearer challenge, you can configure automatic PKCE login:

```toml
[[mcp_servers]]
name      = "github"
transport = "http"
url       = "https://mcp.example.com/mcp/"

  [mcp_servers.oauth]
  client_id     = "…"
  authorize_url = "https://github.com/login/oauth/authorize"
  token_url     = "https://github.com/login/oauth/access_token"
  scopes        = ["read:user", "repo"]
  callback_port = 0   # 0 = random free port; set explicitly if the server requires a fixed one
```

Other fields:

- `disabled = true`: keep the configuration but skip launching it.
- `defer_tools = true`: put that server's tools into the deferred catalog — only tool names appear in the system prompt; full JSON Schemas are loaded on demand when the model needs them. Suited to servers with huge tool sets (Slack / GitHub / Notion and the like).

Diagnostic commands:

```sh
biu mcp list          # launch each server and list its tools
biu mcp probe <name>  # restart a single server and dump tools/list
```

In the REPL, use `/mcp` to see running servers and tools, and `/mcp <name>` to drill down.

> [!TIP]
> MCP tools are also governed by permission rules: a server-level rule like `mcp__github` matches all tools of that server, while `mcp__github__create_issue` targets a single tool. See [permissions.md](permissions.md#mcp-tool-rules).

## Headless mode

```sh
biu --headless --prompt "Summarize this repository's architecture"   # plain-text output
biu --headless --json --prompt "…"                    # JSONL event stream on stdout
echo "…" | biu --headless                             # the prompt can also be read from stdin
```

`--json` emits AG-UI-compatible JSONL events (`RUN_STARTED`, `TEXT_MESSAGE_*`, tool calls and permission prompts, etc.) for consumption by GUIs / CI / SDKs.

The permission policy for unattended runs is controlled with `--permission-policy`:

| Value | Behavior |
|-------|----------|
| `deny` (default) | Every prompt is denied — fail rather than hang |
| `allow` | Allow everything |
| `stdin` | Each prompt asks one line in the terminal: `a` allows once, `s` always allows, anything else denies |
| `stdin-json` | For GUIs: emits a `PERMISSION_ASK` JSON event on stdout (with suggested shortcuts) and reads a one-line JSON decision (`allow` / `deny` / `always`) from stdin |

You can also set the engine permissions mode directly with `--permission-mode default|acceptEdits|bypassPermissions` (if omitted, it falls back to `defaultMode` from settings.json).

## IDE integration: `biu bridge`

`biu bridge` exposes the agent over HTTP + WebSocket to an IDE or a remote UI:

```sh
biu bridge --listen :8088 --auth-token <random-string>
```

- `--listen`: listen address, default `:8088`; pass `:0` to auto-assign a port — the actual address is printed to stderr.
- `--auth-token`: when non-empty, every request must carry `Authorization: Bearer <token>`; leave empty = no authentication (local development only).

Each session maps to an independently constructed agent; state is not shared across clients.

### HTTP routes

| Route | Description |
|-------|-------------|
| `POST /v1/code/sessions` | Create a session, returns `{"id": …}` |
| `POST /v1/code/sessions/:id/messages` | Submit a conversation turn, body is `{"prompt": "…"}`; submitting aborts the session's previous in-flight turn |
| `GET /v1/code/sessions/:id/ws` | WebSocket for streaming events (SDK Protocol v1 frames) |
| `GET /v1/code/sessions/:id/cost` | Cost snapshot of the session (JSON) |
| `POST /v1/code/sessions/:id/compact` | Manually compact the history |
| `POST /v1/code/sessions/:id/attachments` | Upload an attachment |
| `DELETE /v1/code/sessions/:id` | Close the session |
| `GET /v1/code/ws` | Shared WebSocket for the coding module (terminal PTY / Git / files) |

### Resume after disconnect

Each session keeps a ring buffer of the last 256 events. On reconnect, a client calls `GET /v1/code/sessions/:id/ws?last_event_id=N` and the missed frames are replayed from that point — no events are lost.

### Permission prompts over WebSocket

When the engine needs authorization, the bridge sends a `can_use_tool` control request to the client over WebSocket; the client replies `{"behavior": "allow"}` or `{"behavior": "deny"}`. **No reply within 30 seconds is an automatic deny** (safe default); a dropped connection likewise denies immediately.

### VS Code extension

The official VS Code extension automatically spawns `biu bridge` (random port + a per-session one-time token) and provides a session panel, send-selection, cancel-turn, and other commands. The extension settings let you override the binary path (`biu.binaryPath`), the port (`biu.bridgePort`), and the initial permissions mode (`biu.permissionMode`).

## Daemon: `biu serve`

`biu serve` is a superset of `biu bridge`, designed for desktop / long-running scenarios — bridge HTTP service + health checks + optional remote-scheduling registration, combined into one long-lived process:

```sh
biu serve --port 0 --pid-file ~/.biumind/biu.pid
```

- The first stdout line prints `BIU_BRIDGE_URL=http://127.0.0.1:<port>`; the parent process parses this line to learn the actual port.
- `GET /healthz`: liveness probe (returns `{"ok":true,…}`); `GET /metrics`: Prometheus metrics.
- `--pid-file`: PID-file protection — a live `biu serve` is taken over by the new instance (hot restart); if an unrelated process holds the port, startup is refused. When started with a PID file, it also watches its parent process and cleans up automatically when the parent exits, avoiding orphan processes.
- Logs go to both stderr and `~/.biu/logs/daemon.log` (auto-truncated past 10 MB), so crash post-mortems have something to work from.

### Registering as a remotely schedulable execution environment

With `--register`, the machine also registers itself with the server as an agent runtime (`biu_daemon`); remote clients can schedule your machine as an execution target:

```sh
BIUMIND_PAT=<pat> biu serve --register --brain-url https://your-biumind.example.com
```

| Flag | Description |
|------|-------------|
| `--register` | Enable the agent worker (registration + long-polling for jobs) |
| `--brain-url` | Server URL (default: `BIUMIND_BRAIN_URL` or `[model-relay].endpoint`) |
| `--identity-url` | identity service URL (default: same origin as brain) |
| `--allowed-roots` | Filesystem roots this daemon may touch (repeatable; default is the startup directory only) |
| `--tool-policy` | Capability floor: `readonly` \| `workspace-write` (default) \| `full` |

On successful registration, stdout prints `BIU_DAEMON_ENV_ID=<id>`. While running, the desktop client can hot-push a new access token via `POST /internal/token` without restarting the daemon. That endpoint does not use `--auth-token` authentication and is by design only for a same-machine parent process — so when `biu serve` listens externally, bind it to a loopback address.

> [!NOTE]
> The single-purpose entry points remain: use `biu bridge` when you only need the local HTTP bridge, and `biu agent worker` when you only need the worker. `biu serve` reuses the same implementation internally; there is no new protocol.
