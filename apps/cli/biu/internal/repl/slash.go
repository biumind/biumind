// Slash-command catalog + matcher.
//
// Typing "/" in the input opens a dropdown of available commands.
// Up/Down navigates, Enter selects (replaces the input). Tab also
// completes the highlighted command. Typing more chars filters by
// prefix.

package repl

import "strings"

type SlashCmd struct {
	Name        string // "/help"
	Args        string // "[topic]"
	Description string
}

var slashCmds = []SlashCmd{
	{Name: "/help", Description: "show this list"},
	{Name: "/init", Args: "[--force|--dry-run]", Description: "scan the cwd + write a starter BIUMIND.md (build/test/lint commands prefilled)"},
	{Name: "/memory", Args: "[list|reload]", Description: "show BIUMIND.md + auto-memory state, or `reload` to re-read after edits"},
	{Name: "/remember", Args: "[-t <type>] <text>", Description: "save a memory under ~/.biumind/memory (default type=user)"},
	{Name: "/agents", Args: "[create <name> [--scope user|project] [--from <preset>] [--force]]", Description: "list registered sub-agent types, or scaffold a new one"},
	{Name: "/todo", Description: "print the in-session todo checklist"},
	{Name: "/resume", Args: "[#n|latest|<id>]", Description: "replay a saved session — bare form prints a numbered picker"},
	{Name: "/rename", Args: "[<title>|clear]", Description: "label the current session for easier picking in /resume / /sessions"},
	{Name: "/commit", Args: "[--dry-run|--no-stage|-m \"msg\"]", Description: "stage + LLM-draft a Conventional Commits message + commit"},
	{Name: "/pr", Args: "[--dry-run|--no-push|--draft|--base <br>|--title <t>]", Description: "push branch + LLM-draft PR title/body + open via gh"},
	{Name: "/issue", Args: "[<n>|comment <n> \"x\"|close <n>]", Description: "list / view / comment / close GitHub issues via gh"},
	{Name: "/pr-comments", Args: "[<n>]", Description: "show PR review comments (current branch when omitted)"},
	{Name: "/upgrade", Args: "[run|check]", Description: "upgrade biu via brew/go-install/snap based on detected install method"},
	{Name: "/tag", Args: "[<name> [-m \"msg\"|--auto [--from <prev>]]]", Description: "list / create git tags; --auto drafts a changelog via LLM"},
	{Name: "/env", Args: "[<filter>]", Description: "show biu-relevant env vars (KEY/TOKEN/SECRET auto-redacted)"},
	{Name: "/feedback", Args: "[\"summary\"|--print]", Description: "open a prefilled GitHub issue with biu version + session diagnostics"},
	{Name: "/break-cache", Description: "force the next request to skip the prompt cache (debugging-only)"},
	{Name: "/sessions", Description: "list recent saved sessions"},
	{Name: "/tasks", Args: "[list|output <id> [n]|kill <id>|killall]", Description: "list / inspect / terminate background Bash tasks"},
	{Name: "/mcp", Args: "[<server>]", Description: "list connected MCP servers + their tools (drill: /mcp <name>)"},
	{Name: "/plugin", Args: "[<name>|enable <n>|disable <n>|reload]", Description: "list / inspect plugins; enable / disable persists to settings.json"},
	{Name: "/doctor", Description: "health self-check — runtime / git / shell / engine / MCP servers"},
	{Name: "/permissions", Description: "show active permission rules + mode (read-only)"},
	{Name: "/add-dir", Args: "<path> [--remember]", Description: "register an extra working directory; --remember writes to .biumind/settings.local.json"},
	{Name: "/remove-dir", Args: "<path>", Description: "drop a working directory; persisted entries also removed from settings.json"},
	{Name: "/hooks", Args: "[<event-substring>]", Description: "list registered hooks across events; filter by event name when given an arg"},
	{Name: "/rewind", Args: "[<uuid> [--dry-run]]", Description: "list captured file snapshots; restore a uuid's pre-message state"},
	{Name: "/release-notes", Args: "[full|<substring>]", Description: "show biu release notes (last 80 lines by default)"},
	{Name: "/export", Args: "<path> [--format md|json|anthropic-replay]", Description: "write the current session transcript to a file"},
	{Name: "/login", Description: "show OAuth token state (not signed in / signed in + expiry)"},
	{Name: "/logout", Description: "delete locally-stored OAuth tokens (does not revoke upstream)"},
	{Name: "/branch", Description: "git branch state — current branch, upstream, dirty status, recent commits"},
	{Name: "/ide", Description: "show IDE bridge endpoint + setup hint (start with `biu bridge`)"},
	{Name: "/share", Args: "[md|json|<path>]", Description: "export session to tmp + copy path to clipboard"},
	{Name: "/install", Description: "biu binary diagnostics — version, commit, install method, update command"},
	{Name: "/effort", Args: "[high|medium|low|<model-id>]", Description: "switch reasoning depth (Opus/Sonnet/Haiku) — bare form shows the current tier"},
	{Name: "/fast", Description: "shortcut for /effort low — switch to the fastest cheap model"},
	{Name: "/usage", Args: "[today|week|month|all] [<model-prefix>]", Description: "historical token + USD totals from ~/.biu/usage.jsonl"},
	{Name: "/diff", Args: "[staged|<ref>]", Description: "git diff vs HEAD / staged / a branch range — compact --stat output"},
	{Name: "/copy", Args: "[code|<pattern>]", Description: "copy the last assistant text (or code block / pattern match) to clipboard"},
	{Name: "/stats", Description: "current session statistics — duration, messages, tokens, files"},
	{Name: "/summary", Description: "structured paragraph of what this session covered (no LLM call)"},
	{Name: "/onboarding", Description: "first-run guide for new biu users"},
	{Name: "/theme", Args: "[dark|light|system]", Description: "switch colour palette"},
	{Name: "/trust", Args: "[here|session|add <p>|remove <p>]", Description: "manage which directories are trusted to run shell hooks / status-line scripts"},
	{Name: "/plan", Args: "[list|show <id>|diff|approvals]", Description: "list / print plans, diff plan vs actual tool calls, or audit batch approvals"},
	{Name: "/ultraplan", Args: "<task>", Description: "spawn the Plan sub-agent to design an implementation plan"},
	{Name: "/review", Args: "[scope]", Description: "spawn the CodeReview sub-agent — bare form reviews the current branch diff"},
	{Name: "/verify", Args: "[scope]", Description: "spawn the Verification sub-agent — runs the change to find what reading missed; ends with VERDICT: PASS/FAIL/PARTIAL"},
	{Name: "/clear", Description: "wipe history (start fresh)"},
	{Name: "/compact", Description: "summarise old turns to save context"},
	{Name: "/cost", Args: "[--by-tool]", Description: "show running token + $ usage; --by-tool lists per-tool calls/elapsed/bytes/errors"},
	{Name: "/telemetry", Args: "[tail [N]|export <path>|enable <endpoint>|disable]", Description: "show telemetry status; tail / export the events log"},
	{Name: "/workflow", Args: "[<name> [args]|show <name>]", Description: "list / preview / dispatch user-defined multi-step workflows"},
	{Name: "/model", Args: "<id>", Description: "switch model for this session"},
	{Name: "/output-style", Args: "<name>", Description: "switch output style (concise / explanatory / …)"},
	{Name: "/mode", Args: "<mode>", Description: "switch permission mode (default / acceptEdits / plan / bypass)"},
	{Name: "/reload", Description: "force-reload settings.json (permissions; hooks need restart)"},
	{Name: "/quit", Description: "exit"},
}

// matchSlash returns the catalog filtered by prefix. Always returns
// the full list when q == "/" so a fresh "/" pops the whole menu.
//
// `extra` carries dynamically-discovered commands (user-defined
// markdown commands under ~/.biumind/commands/) so they show up in
// the dropdown alongside built-ins. Pass nil when extras aren't
// available — the matcher silently skips them.
func matchSlash(q string, extra []SlashCmd) []SlashCmd {
	q = strings.TrimSpace(q)
	all := slashCmds
	if len(extra) > 0 {
		all = make([]SlashCmd, 0, len(slashCmds)+len(extra))
		all = append(all, slashCmds...)
		all = append(all, extra...)
	}
	if q == "" || q == "/" {
		return all
	}
	out := make([]SlashCmd, 0, len(all))
	for _, c := range all {
		if strings.HasPrefix(c.Name, q) {
			out = append(out, c)
		}
	}
	return out
}

// isSlashTrigger returns true when the buffer should pop the slash
// panel: it starts with "/" and contains no space (a complete command
// followed by an arg should hide the panel).
func isSlashTrigger(s string) bool {
	if !strings.HasPrefix(s, "/") {
		return false
	}
	return !strings.Contains(s, " ")
}
