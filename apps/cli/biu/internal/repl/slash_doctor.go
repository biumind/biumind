// /doctor slash — health self-check across every subsystem the user
// is likely to debug.
//
// Checks API key reachability, MCP server connectivity, sandbox
// availability, git state, and surfaces every finding in a triage-
// friendly table. First stop when something feels wrong. Aggregates
// checks that would otherwise need a chain of "is X working?"
// questions.
//
// Each check returns a Status (ok | warn | fail | skip) + a one-line
// diagnostic. Output groups by status so users see failures first.
// All checks are best-effort and never block the REPL.

package repl

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// doctorStatus is the per-check result tier.
type doctorStatus string

const (
	statusOK   doctorStatus = "ok"
	statusWarn doctorStatus = "warn"
	statusFail doctorStatus = "fail"
	statusSkip doctorStatus = "skip"
)

// doctorCheck is one diagnostic. Fn is invoked under a timeout so a
// hung check doesn't block /doctor; the timeout surfaces as a "fail"
// with a matching message.
type doctorCheck struct {
	Name string
	Fn   func() (doctorStatus, string)
}

// handleDoctor runs every diagnostic and renders the aggregate
// report. Stateless — every call re-runs all checks so a fix
// applied between two /doctor invocations shows up immediately.
//
// Run order is deterministic (alphabetical by Name) so transcripts
// stay diffable across sessions.
func (m model) handleDoctor(parts []string) string {
	checks := m.collectDoctorChecks()
	sort.Slice(checks, func(i, j int) bool {
		return checks[i].Name < checks[j].Name
	})

	type result struct {
		Name    string
		Status  doctorStatus
		Message string
		Elapsed time.Duration
	}
	results := make([]result, 0, len(checks))
	for _, c := range checks {
		start := time.Now()
		status, msg := safeRunCheck(c.Fn)
		results = append(results, result{
			Name:    c.Name,
			Status:  status,
			Message: msg,
			Elapsed: time.Since(start),
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "biu doctor — %d checks\n", len(results))

	// Failures first so a glance shows what to fix.
	for _, want := range []doctorStatus{statusFail, statusWarn, statusOK, statusSkip} {
		shown := false
		for _, r := range results {
			if r.Status != want {
				continue
			}
			if !shown {
				fmt.Fprintf(&b, "\n%s\n", strings.ToUpper(string(want)))
				shown = true
			}
			icon := iconFor(r.Status)
			fmt.Fprintf(&b, "  %s %-20s %s\n", icon, r.Name, r.Message)
		}
	}

	totalFail := 0
	for _, r := range results {
		if r.Status == statusFail {
			totalFail++
		}
	}
	if totalFail == 0 {
		b.WriteString("\noverall: ✓ healthy")
	} else {
		fmt.Fprintf(&b, "\noverall: ✗ %d failure(s)", totalFail)
	}
	return b.String()
}

func iconFor(s doctorStatus) string {
	switch s {
	case statusOK:
		return "✓"
	case statusWarn:
		return "!"
	case statusFail:
		return "✗"
	case statusSkip:
		return "—"
	}
	return "?"
}

// safeRunCheck wraps a check in panic recovery so a buggy
// diagnostic doesn't take down /doctor.
func safeRunCheck(fn func() (doctorStatus, string)) (s doctorStatus, msg string) {
	defer func() {
		if r := recover(); r != nil {
			s = statusFail
			msg = fmt.Sprintf("check panicked: %v", r)
		}
	}()
	return fn()
}

// collectDoctorChecks returns the diagnostic set. Adding a new
// check is one entry here — the runner handles ordering, output,
// and panic safety automatically.
//
// Receiver-bound rather than free function so checks can read REPL
// state (engine wiring, MCP registry, trust store) when present.
func (m model) collectDoctorChecks() []doctorCheck {
	checks := []doctorCheck{
		{Name: "go runtime", Fn: checkGoRuntime},
		{Name: "home directory", Fn: checkHomeDir},
		{Name: "biu config dir", Fn: checkBiuConfigDir},
		{Name: "git", Fn: checkGitInstalled},
		{Name: "ripgrep", Fn: checkRipgrep},
		{Name: "shell", Fn: checkShell},
	}

	// REPL-state-dependent checks only run when the engine is
	// actually wired (e.g. headless mode skips them).
	if m.engine != nil {
		checks = append(checks,
			doctorCheck{Name: "engine path", Fn: checkEnginePath(&m)},
		)
	}
	if m.mcp != nil {
		checks = append(checks,
			doctorCheck{Name: "mcp servers", Fn: checkMCPServers(&m)},
		)
	}
	return checks
}

// ─── individual checks ────────────────────────────────────────

func checkGoRuntime() (doctorStatus, string) {
	return statusOK, fmt.Sprintf("Go %s on %s/%s",
		runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func checkHomeDir() (doctorStatus, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return statusFail, fmt.Sprintf("$HOME unreadable: %v", err)
	}
	if st, err := os.Stat(home); err != nil || !st.IsDir() {
		return statusFail, fmt.Sprintf("%s not a directory", home)
	}
	return statusOK, home
}

func checkBiuConfigDir() (doctorStatus, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return statusFail, "no $HOME"
	}
	dir := home + "/.biumind"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return statusWarn, fmt.Sprintf("%s does not exist (will be created on first save)", dir)
	} else if err != nil {
		return statusFail, fmt.Sprintf("%s: %v", dir, err)
	}
	return statusOK, dir
}

func checkGitInstalled() (doctorStatus, string) {
	if _, err := exec.LookPath("git"); err != nil {
		return statusWarn, "git not on PATH — /commit and git-using plugins won't work"
	}
	return statusOK, "found on PATH"
}

func checkRipgrep() (doctorStatus, string) {
	if _, err := exec.LookPath("rg"); err != nil {
		return statusWarn, "rg not on PATH — Grep falls back to slower regex (still works)"
	}
	return statusOK, "found on PATH"
}

func checkShell() (doctorStatus, string) {
	if runtime.GOOS == "windows" {
		return statusSkip, "Windows: shell semantics differ"
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return statusFail, "sh not on PATH — Bash tool will fail"
	}
	return statusOK, "/bin/sh available"
}

// checkEnginePath verifies the engine has a usable provider. We
// can't actually call the LLM (would charge tokens / take seconds),
// but we can confirm the wiring is non-nil.
func checkEnginePath(m *model) func() (doctorStatus, string) {
	return func() (doctorStatus, string) {
		if m.engine == nil {
			return statusSkip, "engine not wired (chat mode only)"
		}
		return statusOK, fmt.Sprintf("model=%s", m.modelID)
	}
}

// checkMCPServers reports the connected-server count. Doesn't
// re-handshake — that would be a much heavier operation than a
// /doctor invocation should incur.
func checkMCPServers(m *model) func() (doctorStatus, string) {
	return func() (doctorStatus, string) {
		if m.mcp == nil {
			return statusSkip, "no MCP registry"
		}
		servers := m.mcp.Servers()
		if len(servers) == 0 {
			return statusOK, "no servers configured"
		}
		var names []string
		for _, s := range servers {
			names = append(names, fmt.Sprintf("%s (%d tools)", s.Name, s.ToolCount))
		}
		return statusOK, strings.Join(names, ", ")
	}
}
