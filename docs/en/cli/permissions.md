# Permissions and Hooks

In `biu`, every tool call (file reads and writes, Bash commands, MCP tools) passes through a permissions gate before execution. Three components jointly decide the outcome:

1. **Mode** — the session-level coarse-grained default behavior;
2. **Rules** — fine-grained allow / deny / ask matching in the form `Tool(content)`;
3. **Hooks** — user scripts that run at lifecycle points; they can audit, block, and rewrite.

Rules take precedence over a mode's default behavior: an allow rule always beats the mode's "ask", and a deny rule always beats the mode's "allow".

## The three settings.json layers

Permission configuration lives in settings.json (not `~/.biu/config.toml`) and is loaded and merged layer by layer:

| Layer | Path | Purpose |
|-------|------|---------|
| user | `~/.biumind/settings.json` | Global personal preferences |
| project | `<project>/.biumind/settings.json` | Shared with the team, committed to git |
| local | `<project>/.biumind/settings.local.json` | Per-machine overrides, add to `.gitignore` |

```json
{
  "permissions": {
    "allow": [
      "Bash(git status)",
      "Bash(go build:*)",
      "Bash(go test:*)",
      "Edit(/repo/**)"
    ],
    "deny": [
      "Bash(rm -rf /)"
    ],
    "ask": [
      "Bash(git push:*)"
    ],
    "defaultMode": "default",
    "additionalDirectories": ["../docs", "${PROJECT_ROOT}/bench"]
  }
}
```

- The three rule arrays are merged and evaluated together (see "Decision order" below); there is no such thing as "one layer's allow beating another layer's deny" — deny is always strongest, no matter which layer it is written in.
- `defaultMode` is taken from the most specific layer: local > project > user.
- `additionalDirectories` appends read-write working directories (pairs with the REPL's `/add-dir <path> --remember` and the startup flag `--add-dir`).

In the REPL, use `/permissions` to view the rules and mode currently in effect, `/mode <name>` to switch modes, and `/reload` to reload settings.json.

## Modes

| Mode | Behavior |
|------|----------|
| `default` | Reads are allowed automatically; destructive operations ask on first use |
| `acceptEdits` | File edits / writes are allowed automatically; Bash and the rest still go through normal evaluation |
| `plan` | Read-only thinking mode — every non-read-only tool call is denied (`EnterPlanMode` / `ExitPlanMode` themselves are exempt) |
| `bypassPermissions` | Everything is allowed automatically — dangerous; use only in trusted environments |
| `dontAsk` | Every call that would ask is denied outright (the "panic" mode) |

Legacy vocabulary is accepted: `ask` ≡ `default`, `auto_edit` ≡ `acceptEdits`, `full_access` ≡ `bypassPermissions`.

In non-default modes, the REPL status bar is tinted as a whole and shows a badge as a peripheral visual cue: plan is blue (`❙❙`), acceptEdits is amber (`⏵⏵ Accept`), bypass is red (`⏵⏵ Bypass`), dontAsk is gray (`⏵⏵ DontAsk`).

### The Plan mode lifecycle

Plan mode is not just a permissions flip — the engine remembers the mode from before entry and restores it on exit:

1. The model calls `EnterPlanMode`, or you run `/mode plan`: the current mode is saved and switched to `plan`.
2. The model does its research; every non-read-only call is denied.
3. The model calls `ExitPlanMode` (carrying a markdown plan and optional `allowedPrompts`), or you manually run `/mode <something else>`: the saved mode is restored.

`allowedPrompts` is a batch approval mechanism — the model pre-declares in the plan the categories of actions the execution phase will need:

```json
{
  "plan": "## Steps\n1. Run tests\n2. Build",
  "allowedPrompts": [
    { "tool": "Bash", "prompt": "go test ./..." },
    { "tool": "Bash", "prompt": "go build" }
  ]
}
```

Once the plan is approved, these entries become session-level authorizations and the execution phase stops asking one by one (explicit deny rules can still veto). Approved plans are persisted to `~/.biu/plans/<session-id>.md` for later review and for `--resume` continuity; after `/compact` compacts the history, the plan is re-injected as a system attachment so the model doesn't "forget" the approach it committed to. Manage plans in the REPL with `/plan list` / `/plan show <id>` / `/plan diff` / `/plan approvals`; the command-line counterpart is the `biu plan` subcommand.

## Rule syntax

A rule is a string written into the `allow` / `deny` / `ask` arrays:

```text
Tool                  # covers every call of that tool
Tool(content)         # tool + content qualifier (command, path, pattern, …)
Tool(prefix:*)        # prefix match: the prefix itself or "prefix <any arguments>"
Tool(pattern*)        # wildcard: * matches any characters
Tool(\(literal\))     # escape literal parentheses in the content with \
```

- **A bare `Tool` without parentheses** matches every call of that tool.
- **Tool names are case-insensitive**: `bash` is equivalent to `Bash`.
- The legacy `tool:detail` colon syntax is still accepted and is semantically equivalent to the prefix match `tool(detail:*)`.

### Matching semantics per tool family

| Tool | What content matches against | Examples |
|------|------------------------------|----------|
| `Bash` | The full command string | `Bash(go test)` matches exactly that command; `Bash(go:*)` matches `go` and any `go <subcommand>`; `Bash(git push*)` is a wildcard match |
| `Edit` / `Write` / `Read` / `Glob` | File path / pattern, glob style | `Edit(./src/**)` matches files under src at any depth; `*` one level, `**` across levels, `?` a single character |
| `Grep` | The search pattern (then path) | `Grep(TODO)` |
| Other tools (including MCP) | Globs are tried in turn against the `path` / `file_path` / `pattern` / `command` / `url` / `query` fields | `WebFetch(https://api.example.com/*)` |

### MCP tool rules

Rules for MCP tools use the tool's qualified name:

- `mcp__github__create_issue` — matches exactly one tool;
- `mcp__github` — a server-level rule matching all `mcp__github__*` tools (writing `mcp__github__*` is equivalent).

### Decision order

Every tool call is evaluated in the following order; the first hit returns:

1. `bypassPermissions` mode → allow;
2. `plan` mode and non-read-only (and not a plan-switching tool) → deny;
3. the session's "always allow" authorization cache → allow;
4. a hit in plan-approved `allowedPrompts` (with no deny rule hit) → allow;
5. **a deny rule hit → deny**;
6. **an ask rule hit → ask**;
7. **an allow rule hit → allow**;
8. read-only and non-destructive → allow (path-based tools must stay inside allowed working directories, otherwise they fall through to asking);
9. editing tools under `acceptEdits` mode → allow;
10. `dontAsk` mode → deny;
11. otherwise → ask.

Two of these steps — "read-only auto-allow" and "file writes" — are fronted by a **working directory gate**: for path-based tools such as `Read` / `Glob` / `Grep` / `Edit` / `Write` / `MultiEdit` / `NotebookEdit`, if the target path is not inside any registered working directory (startup directory + `additionalDirectories` + `/add-dir`), the call goes straight to asking, even if a rule or mode would have allowed it.

### The REPL prompt box

When asked, a confirmation box pops up above the input field with shortcuts: `a` allow once; `shift+a` (or `s`) allow and remember (written to the session authorization cache); `d` deny; `q` / `esc` deny and interrupt the current turn. Prompts with a suggestion shortcut (e.g. `w` = "allow and add to working directories") apply the suggestion and allow the call when pressed.

### The legacy [permissions] section of config.toml

The `[permissions]` section in `~/.biu/config.toml` (`mode` + `allowlist`) is the legacy interface; it still works, but rules are best consolidated into settings.json. The section has three additional plan-suggestion fields: `plan_drift_threshold` (plan-drift prompt threshold; 0 = prompt on first drift, negative = observe only, never prompt), `suggest_plan_for` (keyword list that triggers the "consider plan mode" suggestion), and `suggest_plan_disabled` (turns the suggestion off).

## Hooks

Hooks are shell commands you register at lifecycle points. They are written in the `hooks` section of settings.json; same-named events across the three layers are **all executed** (merged as a union):

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "scripts/audit.sh", "timeout": 30 }
        ]
      },
      {
        "matcher": "Edit|Write",
        "hooks": [{ "type": "command", "command": "scripts/protect.sh" }]
      }
    ],
    "UserPromptSubmit": [
      { "hooks": [{ "type": "command", "command": "jq -r .prompt >> /tmp/biu-prompts.log" }] }
    ]
  }
}
```

### Configuration fields

| Field | Description |
|-------|-------------|
| `matcher` | A regex (Go regexp syntax, `\|` alternation supported) matching the event identifier: `PreToolUse` / `PostToolUse` match tool names, `Notification` matches notification types, `SessionStart` matches the source (`startup` / `resume` / `compact`); omitted = match everything |
| `type` | `command` (fork a subprocess) or `internal` (for built-in plugins); `prompt` / `agent` / `http` are reserved values, currently not executed |
| `command` | The command to run |
| `shell` | `bash` / `sh` / `pwsh`, default `sh` |
| `timeout` | Seconds; default 60 |
| `if` | An optional precondition rule |

### The command contract

- **stdin**: a one-line JSON object (event-specific fields: `tool_name`, `tool_input`, `prompt`, etc.), terminated by a newline.
- **stdout**: optional JSON; if it parses as a decision object, the engine honors it (see below).
- **Exit code 0**: success; if stdout is JSON, it is consumed.
- **Exit code 2**: soft block — the tool call / submission is aborted, and stderr is fed back to the model so it can adjust.
- **Other exit codes**: non-fatal warning; stderr is shown to the user.

Fields supported by the decision object (stdout JSON):

```json
{
  "block": true,
  "reason": "Deleting the main branch is not allowed",
  "additionalContext": "Hint appended to the system context (SessionStart / UserPromptSubmit)",
  "replacePrompt": "The rewritten user prompt (UserPromptSubmit)"
}
```

`block: true` is equivalent to exit code 2; empty fields mean "no opinion".

### Event list

Tools and permissions: `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionDenied`

Conversation turns: `UserPromptSubmit`, `Stop`, `StopFailure`, `Notification`

Subagents: `SubagentStart`, `SubagentStop`, `TeammateIdle`

Tasks and files: `TaskCreated`, `TaskCompleted`, `FileChanged`, `CwdChanged`

Sessions and compaction: `SessionStart`, `SessionEnd`, `PreCompact`, `PostCompact`

In the REPL, `/hooks [<event-name-fragment>]` lists all currently registered hooks (with their source layer).

### The trust gate

Shell hooks and status-line scripts amount to arbitrary command execution. To prevent a malicious repository's `.biumind/settings.json` from carrying hostile hooks, biu executes hooks only in **trusted directories**: grant trust on first use in a new directory with `/trust here` (persisted to `~/.biumind/trust.json`), `/trust session` trusts for the current session only, and `/trust remove <path>` revokes. In scripted environments like CI, the environment variable `BIU_TRUST=1` trusts all directories for one run.

## Permissions in headless / SDK scenarios

Unattended runs have no interactive terminal; specify the policy with `--permission-policy`: `deny` (default — deny everything, fail rather than hang), `allow`, `stdin` (ask per prompt in the terminal), `stdin-json` (emit a `PERMISSION_ASK` JSON event on stdout, read a JSON decision from stdin, for GUI consumption; supports the three decisions `allow` / `deny` / `always` plus suggested shortcuts). See [usage.md](usage.md#headless-mode) for details.
