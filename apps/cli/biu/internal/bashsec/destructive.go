// Package bashsec is a leaf package containing read-only analysis
// helpers for shell commands. Lives outside tools/web so the engine
// runner can import it for permission-prompt enrichment without
// pulling in the whole tool catalog (cycle).
//
// Today the package exports one helper: Warning(cmd) reports a
// human-readable note when the command matches a known destructive
// pattern (rm -rf, git reset --hard, terraform destroy, …). The
// permission layer pre-pends this to the ask dialog so the user
// sees the cost of approval, not just the command.
//
// Patterns are advisory: matching does NOT change the permission
// decision; it only enriches the dialog text.

package bashsec

import "regexp"

// destructivePattern is one (regex, warning) pair. Order matters —
// the first match wins, so put narrower patterns above broader ones
// (e.g. `rm -rf` before bare `rm -r`).
type destructivePattern struct {
	pattern *regexp.Regexp
	warning string
}

// patterns is the full table. Each regex compiles once at package
// start via init.
var patterns = []destructivePattern{
	// ── git: data loss / hard to reverse ──────────────────
	{
		regexp.MustCompile(`\bgit\s+reset\s+--hard\b`),
		"may discard uncommitted changes",
	},
	{
		regexp.MustCompile(`\bgit\s+push\b[^;&|\n]*[ \t](--force|--force-with-lease|-f)\b`),
		"may overwrite remote history",
	},
	{
		regexp.MustCompile(`\bgit\s+clean\b(?:[^;&|\n]*-[a-zA-Z]*f)`),
		"may permanently delete untracked files",
	},
	{
		regexp.MustCompile(`\bgit\s+checkout\s+(?:--\s+)?\.[ \t]*(?:$|[;&|\n])`),
		"may discard all working tree changes",
	},
	{
		regexp.MustCompile(`\bgit\s+restore\s+(?:--\s+)?\.[ \t]*(?:$|[;&|\n])`),
		"may discard all working tree changes",
	},
	{
		regexp.MustCompile(`\bgit\s+stash[ \t]+(?:drop|clear)\b`),
		"may permanently remove stashed changes",
	},
	{
		regexp.MustCompile(`\bgit\s+branch\s+(?:-D[ \t]|--delete\s+--force|--force\s+--delete)\b`),
		"may force-delete a branch",
	},
	// ── git: safety bypass ────────────────────────────────
	{
		regexp.MustCompile(`\bgit\s+(?:commit|push|merge)\b[^;&|\n]*--no-verify\b`),
		"may skip safety hooks",
	},
	{
		regexp.MustCompile(`\bgit\s+commit\b[^;&|\n]*--amend\b`),
		"may rewrite the last commit",
	},
	// ── filesystem: rm variants ───────────────────────────
	// `-rf` / `-fr` / `-Rf` etc. — narrowest first.
	{
		regexp.MustCompile(`(?:^|[;&|\n]\s*)rm\s+-[a-zA-Z]*[rR][a-zA-Z]*f|(?:^|[;&|\n]\s*)rm\s+-[a-zA-Z]*f[a-zA-Z]*[rR]`),
		"may recursively force-remove files",
	},
	{
		regexp.MustCompile(`(?:^|[;&|\n]\s*)rm\s+-[a-zA-Z]*[rR]`),
		"may recursively remove files",
	},
	{
		regexp.MustCompile(`(?:^|[;&|\n]\s*)rm\s+-[a-zA-Z]*f`),
		"may force-remove files",
	},
	// ── databases ─────────────────────────────────────────
	{
		regexp.MustCompile(`(?i)\b(?:DROP|TRUNCATE)\s+(?:TABLE|DATABASE|SCHEMA)\b`),
		"may drop or truncate database objects",
	},
	{
		regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+\w+[ \t]*(?:;|"|'|\n|$)`),
		"may delete all rows from a database table",
	},
	// ── infrastructure ────────────────────────────────────
	{
		regexp.MustCompile(`\bkubectl\s+delete\b`),
		"may delete Kubernetes resources",
	},
	{
		regexp.MustCompile(`\bterraform\s+destroy\b`),
		"may destroy Terraform infrastructure",
	},
}

// Warning scans `cmd` and returns the first matching destructive
// pattern's warning string, or "" when nothing matches. Cheap — one
// linear scan over a small static table per call.
//
// Returns the warning verbatim from the table (no "Note: " prefix);
// the caller decides how to format the dialog. We leave that decision
// to the runner so headless / SDK callers can render plainly.
func Warning(cmd string) string {
	for _, p := range patterns {
		if p.pattern.MatchString(cmd) {
			return p.warning
		}
	}
	return ""
}
