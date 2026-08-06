---
name: plugin-manifest
description: Reference for the biu plugin manifest schema. Auto-attaches when a plugin.json or .claude-plugin/plugin.json is open or being edited.
paths: ["**/.claude-plugin/plugin.json", "**/plugin.json"]
---

# biu plugin.json reference

The manifest is parsed by `internal/plugins.PluginManifest` and validated at load time. Required fields are non-optional; everything else is best omitted unless you actually use it.

## Required

| field | constraint |
|---|---|
| `name` | `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$` — lowercase, hyphens, 1–64 chars |
| `version` | non-empty string; semver recommended but not enforced |
| `author.name` | non-empty |

## Optional metadata

| field | notes |
|---|---|
| `description` | one line, ≤256 chars; long-form belongs in `README.md` |
| `author.email`, `author.url` | for credit |
| `homepage`, `repository`, `license` | informational |
| `keywords` | string array |

## Component path overrides

Defaults follow convention: `commands/`, `agents/`, `skills/`, `output-styles/` at the plugin root. Override only when you need a non-standard location:

```json
{
  "commandsPath": "src/commands",
  "agentsPath":   "src/agents"
}
```

Paths must be relative + must stay inside the plugin root. `..` and absolute paths are rejected.

## Inline hooks

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash",
      "hooks": [{ "type": "command", "command": "echo hi" }]
    }],
    "Stop": [{ "hooks": [{ "type": "internal", "handler": "myplugin:stop" }] }]
  }
}
```

Two execution shapes:
- `type: "command"` — fork a subprocess. Use `${CLAUDE_PLUGIN_ROOT}` (or `${BIU_PLUGIN_ROOT}`) in the command string for the absolute plugin path.
- `type: "internal"` — invoke a Go handler registered via `hooks.RegisterInternal` (only for plugins shipped inside the biu binary).

External hooks file (preferred for shell handlers): write `hooks/hooks.json` with the same shape under a top-level `hooks` key. The loader merges it when `plugin.json.hooks` is absent.

## MCP servers

```json
{
  "mcpServers": {
    "github": {
      "command": "/usr/local/bin/gh-mcp",
      "args":    ["--token", "${GH_TOKEN}"],
      "env":     { "GH_TOKEN": "..." }
    },
    "linear": {
      "transport": "http",
      "url":       "https://mcp.linear.app",
      "headers":   { "Authorization": "Bearer ${LINEAR_TOKEN}" }
    }
  }
}
```

Plugin MCP servers attach with names namespaced as `<plugin>__<server>` so two plugins can ship `github` MCP servers without colliding.

## Settings

The `settings` field declares plugin-author defaults. Users override per-plugin in `~/.biumind/settings.json` under `plugins.configs.<name>`. Today this is opaque — surfaced via `biu plugin show` but not enforced.

## Claude plugins interop

biu accepts the `~/.claude/plugins/` layout unchanged (PP8a). Manifest keys biu doesn't yet consume (`channels`, `lspServers`, `userConfig`) round-trip without erroring; they show up in `biu plugin show <name>` under "unrecognised manifest keys" so you can spot misalignment.

## Validation

Always run `biu plugin validate <dir>` before publishing. The command reports every schema violation in one pass — fix-once, not fix-rerun.
