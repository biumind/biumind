// /init slash. Detects project type + scaffolds a starter
// BIUMIND.md.

package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/projectinit"
)

// handleInit dispatches the /init subcommands.
//
//	/init             — write BIUMIND.md if it doesn't exist
//	/init --force     — overwrite an existing BIUMIND.md
//	/init --dry-run   — print the rendered template, don't write
//
// The detector is deterministic — same cwd produces the same
// output. Users typically run /init once per project, then edit by
// hand; --force lets them re-run after major project structure
// changes (added a workspace, switched package manager).
func (m model) handleInit(parts []string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return "/init: cannot resolve cwd: " + err.Error()
	}
	force := false
	dryRun := false
	for _, p := range parts[1:] {
		switch p {
		case "--force", "-f":
			force = true
		case "--dry-run", "-n":
			dryRun = true
		default:
			return "/init: unknown flag " + p +
				" (usage: /init [--force | --dry-run])"
		}
	}

	path := filepath.Join(cwd, "BIUMIND.md")
	exists := false
	if _, err := os.Stat(path); err == nil {
		exists = true
	}
	if exists && !force && !dryRun {
		return "/init: BIUMIND.md already exists. " +
			"Re-run `/init --force` to overwrite, or `/init --dry-run` " +
			"to preview without writing."
	}

	detected := projectinit.Detect(cwd)
	body := detected.Render()

	if dryRun {
		var b strings.Builder
		b.WriteString("/init --dry-run: would write BIUMIND.md (")
		fmt.Fprintf(&b, "%d languages detected", len(detected.Languages))
		if len(detected.Notes) > 0 {
			fmt.Fprintf(&b, ", %d note(s)", len(detected.Notes))
		}
		b.WriteString("):\n\n")
		b.WriteString(body)
		return strings.TrimRight(b.String(), "\n")
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "/init: " + err.Error()
	}

	var summary strings.Builder
	if exists {
		summary.WriteString("/init: overwrote BIUMIND.md")
	} else {
		summary.WriteString("/init: wrote BIUMIND.md")
	}
	if len(detected.Languages) > 0 {
		names := make([]string, 0, len(detected.Languages))
		for _, l := range detected.Languages {
			names = append(names, string(l.Language))
		}
		fmt.Fprintf(&summary, " (detected: %s)", strings.Join(names, ", "))
	} else {
		summary.WriteString(" (no manifest detected — fill in the placeholders)")
	}
	summary.WriteString(". Run `/memory reload` to pick it up without restart.")
	return summary.String()
}
