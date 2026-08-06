// /summary + /onboarding + /theme — three small UX slashes that
// round out the catalog.
//
// /summary    — distill the current session into a paragraph the
//               user can paste into a status update / standup.
//               Different from /export (full transcript) and
//               /stats (raw numbers); /summary is "what got done".
// /onboarding — first-run guide for new biu users. Lists the
//               must-know slashes + env vars + plugin entry path.
//               Useful for users dropping in cold or pointing
//               teammates at biu.
// /theme      — toggle between dark / light / system colour
//               palettes. biu's REPL is bubbletea — colour
//               profiles live in `internal/repl/styles.go`.

package repl

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

// ─── /summary ────────────────────────────────────────────────

// handleSummary renders a structured paragraph describing what the
// session covered. Pure-CPU heuristic — no LLM call. Good enough
// for "what was I doing?" recall and "post to standup channel"
// flows; for a real LLM-generated summary use /compact (which
// produces the canonical version).
func (m model) handleSummary(parts []string) string {
	if len(m.history) == 0 {
		return "/summary: no history yet — start a turn first"
	}

	user, asst := countMessages(m.history)
	firstPrompt := firstUserPrompt(m.history)
	lastAssistantBody := lastAssistantText(m.history)

	var b strings.Builder
	b.WriteString("/summary: this session\n\n")

	if firstPrompt != "" {
		fmt.Fprintf(&b, "Started with: %s\n", oneLineSummary(firstPrompt, 100))
	}
	fmt.Fprintf(&b, "%d turns total (%d user, %d assistant)\n", len(m.history), user, asst)

	if m.engine != nil {
		snap := m.engine.Cost().Snapshot()
		fmt.Fprintf(&b, "Tokens: %d in / %d out  USD: $%.4f\n",
			snap.InputTokens, snap.OutputTokens, snap.USD)
	}

	if lastAssistantBody != "" {
		b.WriteString("\nMost recent assistant turn:\n")
		fmt.Fprintf(&b, "  %s\n", oneLineSummary(lastAssistantBody, 200))
	}

	b.WriteString("\nFor a richer model-generated summary use /compact (replaces history with the summary).")
	return strings.TrimRight(b.String(), "\n")
}

// firstUserPrompt returns the body of the first user message in
// history. Empty when history has no user message yet.
func firstUserPrompt(history []client.Message) string {
	for _, m := range history {
		if m.Role == "user" {
			return strings.TrimSpace(m.Content)
		}
	}
	return ""
}

// oneLineSummary collapses a multi-line body to a single-line
// preview, capped at maxLen chars (rune-safe).
func oneLineSummary(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > maxLen {
		return string(r[:maxLen-1]) + "…"
	}
	return string(r)
}

// ─── /onboarding ──────────────────────────────────────────────

// handleOnboarding prints the new-user guide. Static text — no
// state-dependent branches, since onboarding should look the same
// regardless of session state (showing different content per user
// state would make pointing colleagues at it harder).
func (m model) handleOnboarding(parts []string) string {
	return strings.TrimSpace(`/onboarding: welcome to biu

Try these first:
  /help              — full slash command catalog
  /doctor            — health check (run when something feels off)
  /effort low        — switch to the cheapest model for one-off questions
  /compact           — summarise old turns when context fills up
  /export <path>     — write the session transcript to a file

Configuration lives at:
  ~/.biumind/settings.json   — permissions, hooks, plugins, sandbox
  ~/.biumind/plugins/        — drop a plugin dir here, restart biu
  ~/.biumind/skills/         — drop a SKILL.md, auto-loads on next session
  ~/.biumind/memory/         — append-only memory; biu reads it as context
  ~/.biumind/sessionMemory/  — per-session resume notes (auto-maintained)

Useful env vars:
  DISABLE_COMPACT=1                     — turn off auto-compact
  BIU_PROMPT_SUGGEST=0                  — silence input suggestions
  BIU_TIME_BASED_MC=1                   — clear old tool results after 60min idle
  CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=80    — fire compact at 80% instead of default

When in doubt:
  /doctor                               — diagnose
  /release-notes                        — what's new in this build
  /install                              — how this binary was installed`)
}

// ─── /theme ──────────────────────────────────────────────────

// ThemeName captures the available colour profiles. Keep the
// values stable; they're persisted to settings.json.
type ThemeName string

const (
	ThemeDark   ThemeName = "dark"
	ThemeLight  ThemeName = "light"
	ThemeSystem ThemeName = "system"
)

// handleTheme switches the active colour profile or shows the
// current state. The actual style table lives in styles.go;
// this slash mutates m.theme + persists to settings via the
// REPL's existing reload path.
func (m model) handleTheme(parts []string) (model, string) {
	if len(parts) <= 1 {
		current := m.theme
		if current == "" {
			current = string(ThemeSystem)
		}
		return m, fmt.Sprintf("/theme: current = %s\n\nuse /theme dark | light | system", current)
	}
	target := strings.ToLower(strings.TrimSpace(parts[1]))
	switch target {
	case string(ThemeDark), string(ThemeLight), string(ThemeSystem):
		// ok
	default:
		return m, fmt.Sprintf("/theme: unknown theme %q (dark | light | system)", target)
	}
	m.theme = target
	hint := ""
	if target == string(ThemeSystem) {
		hint = fmt.Sprintf(" (auto: detected %s)", detectSystemTheme())
	}
	return m, fmt.Sprintf("/theme: switched to %s%s", target, hint)
}

// detectSystemTheme returns "dark" / "light" based on platform-
// specific signals. Best-effort; returns "dark" as the safe
// default when detection can't decide (most terminal users prefer
// dark, and biu's bubbletea palette is tuned for dark).
func detectSystemTheme() string {
	switch runtime.GOOS {
	case "darwin":
		// macOS exposes AppleInterfaceStyle=Dark when dark mode is on.
		// We'd shell out to `defaults read -g AppleInterfaceStyle`
		// for a real check; for now fall back to dark default.
		return "dark"
	case "linux":
		// GNOME / KDE expose a few env vars; not worth the breadth
		// for a slash that's mostly a settings hint.
		return "dark"
	}
	return "dark"
}
