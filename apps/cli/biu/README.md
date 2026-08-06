# biu — BiuMind agent CLI

`biu` is BiuMind's terminal-first AI coding agent. Same agent loop as
Claude Code, written in Go, ships as a single 15 MB static binary with
no runtime dependencies.

## Install

```sh
brew install biumind/tap/biu
```

Or grab a pre-built binary from
[github Releases](https://github.com/biumind/biumind/releases?q=biu-v):

```sh
tar -xzf biu_*_$(uname -s)_$(uname -m).tar.gz
install -m 0755 biu /usr/local/bin/biu
biu doctor
```

From source (Go 1.22+):

```sh
go install github.com/biumind/biumind/apps/cli/biu/cmd/biu@latest
```

## 30-second start

```sh
# 1. point biu at a provider
mkdir -p ~/.biu && cat > ~/.biu/config.toml <<'EOF'
[default]
mode     = "direct"
provider = "anthropic"
model    = "claude-sonnet-4-6"

[providers.anthropic]
api_key = "sk-ant-..."
EOF

# 2. confirm the environment is healthy
biu doctor

# 3. start coding
biu
```

Type `/help` inside the REPL for the slash-command list. Type a normal
sentence to send it as a turn. Press `Ctrl-C` to cancel a streaming
response. Press `Esc` then `q` to quit.

## What it does

- 🛠 **Tool loop** with built-in `read`, `write`, `edit`, `grep`,
  `glob`, `bash`, plus orchestration tools (`agent`, `task`,
  `worktree`, `lsp`).
- 🧠 **Memory** auto-loaded from `BIUMIND.md` files at four layers
  (user / project / cwd / local).
- 🔒 **Permissions** with `Tool(content)` rules, four modes, and
  pluggable hooks for audit / block / rewrite.
- ↻ **Compact** to summarise long histories into a single system
  message before the context window fills.
- 🌐 **HTTP bridge** (`biu bridge`) for IDE / remote-agent integration
  with SSE streaming + Last-Event-ID resume.
- 📦 **Headless mode** (`biu --headless`) for CI and SDK callers.

## Three deployment modes

| Mode | Best for | How |
|------|----------|-----|
| **direct** | Solo dev, BYO API key | `mode = "direct"` in `~/.biu/config.toml` |
| **byo_endpoint** | Self-hosted LiteLLM / proxy | `mode = "byo_endpoint"`, set `[providers.<name>]` |
| **cloud** | Team via BiuMind model-relay | `mode = "cloud"` with `[model-relay]` block |

## Documentation

- [getting-started.md](../../docs/biu/getting-started.md) — 3-minute tutorial
- [usage.md](../../docs/biu/usage.md) — feature tour
- [commands.md](../../docs/biu/commands.md) — every subcommand & flag
- [biumind-md.md](../../docs/biu/biumind-md.md) — memory file format
- [permissions.md](../../docs/biu/permissions.md) — rule grammar + modes
- [architecture.md](../../docs/biu/architecture.md) — internals

## Embedding

`biu` ships its agent core as a Go SDK at
`github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit`:

```go
ag, _ := biumindkit.New(biumindkit.Options{
    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
    Cwd:    ".",
})
defer ag.Close()
text, _, _ := ag.Run(context.Background(), "what's in main.go?")
fmt.Println(text)
```

## License

Apache 2.0. See [LICENSE](LICENSE).
