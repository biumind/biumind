// /resume helpers — the menu picker and arg resolver.

package repl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// buildResumeMenu prints the inline session picker the bare /resume
// command surfaces: numbered list of recent sessions + the syntax
// the user types to load one. Stateless by design — the REPL doesn't
// need a modal "pending pick" mode, the user just re-issues
// `/resume #<n>` or `/resume <id>` from the menu.
func buildResumeMenu(dir string) string {
	rows, err := session.ListSessions(dir)
	if err != nil {
		return "/resume: " + err.Error()
	}
	if len(rows) == 0 {
		return "/resume: no saved sessions yet"
	}
	const max = 10
	n := len(rows)
	if n > max {
		n = max
	}
	var b strings.Builder
	b.WriteString("Recent sessions — pick one with `/resume #<n>` or `/resume <id>`:\n")
	for i := 0; i < n; i++ {
		r := rows[i]
		preview := r.FirstPrompt
		if preview == "" {
			preview = "(no user prompt yet)"
		}
		fmt.Fprintf(&b, "  #%d  %s  %d msgs  %s\n", i+1, r.ID, r.MessageCount, preview)
	}
	if len(rows) > max {
		fmt.Fprintf(&b, "  …(%d more; use `/resume <id>` for the older entries)\n",
			len(rows)-max)
	}
	b.WriteString("  /resume latest    pick the most recent")
	return strings.TrimRight(b.String(), "\n")
}

// resolveResumeArg accepts the three syntaxes the picker advertises:
//
//   - `latest`   → newest session
//   - `#<n>`     → 1-based index from the menu
//   - `<id>`     → exact session id (existing behaviour)
//
// Returns the resolved Summary or ok=false. Centralised so the slash
// handler stays narrative.
func resolveResumeArg(dir, arg string) (session.Summary, bool) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "latest":
		return session.FindLatest(dir)
	case strings.HasPrefix(arg, "#"):
		n, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
		if err != nil {
			return session.Summary{}, false
		}
		return session.FindByIndex(dir, n)
	default:
		return session.FindByID(dir, arg)
	}
}
