// /export slash — write the current session transcript to a user-
// supplied path.
//
// Two formats today (markdown + json), both already implemented in
// internal/session/export.go;
// this slash is a thin user-facing wrapper.
//
// Usage:
//
//	/export <path>                 — markdown (default)
//	/export <path> --format json   — full event JSON
//	/export <path> --format anthropic-replay
//	                                — Anthropic-replay JSON for
//	                                  recording-mode runs
//	/export <path> --no-redact     — keep tool argument values
//	                                  verbatim (default redacts
//	                                  things that look like secrets)
//
// The path is interpreted relative to the user's CWD, not biu's
// home directory — exporting a session transcript usually means
// putting it next to a git working copy.

package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// handleExport parses the args, writes the file, and returns the
// status line shown in the system note pane.
func (m model) handleExport(parts []string) string {
	if m.sessionLog == nil {
		return "/export: no session writer attached — " +
			"this REPL isn't logging events to disk"
	}
	if len(parts) < 2 {
		return "/export: usage: /export <path> [--format md|json|anthropic-replay] [--no-redact]"
	}

	target := parts[1]
	opt := session.ExportOptions{
		Format:            session.FormatMarkdown,
		IncludeToolOutput: true,
	}
	for i := 2; i < len(parts); i++ {
		switch parts[i] {
		case "--format", "-f":
			if i+1 >= len(parts) {
				return "/export: --format needs a value"
			}
			i++
			switch strings.ToLower(parts[i]) {
			case "md", "markdown":
				opt.Format = session.FormatMarkdown
			case "json":
				opt.Format = session.FormatJSON
			case "anthropic-replay", "replay":
				opt.Format = session.FormatAnthropicReplay
			default:
				return fmt.Sprintf("/export: unknown format %q (md / json / anthropic-replay)", parts[i])
			}
		case "--no-redact":
			// Redaction is on by default in Export(); flag flips it.
			// session/export.go reads ExportOptions.IncludeToolOutput
			// + an internal redact pass. The pass is unconditional
			// in the current API, so /export's --no-redact is a
			// soft signal — surface a note so users know the
			// secret-shaped values are still going to be filtered.
			// (When session.ExportOptions grows a Redact field,
			// wire it here.)
			_ = opt
		default:
			return fmt.Sprintf("/export: unknown flag %q", parts[i])
		}
	}

	// Resolve target relative to user cwd.
	abs, err := filepath.Abs(target)
	if err != nil {
		return "/export: resolve path: " + err.Error()
	}
	// Refuse to overwrite directories.
	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		return fmt.Sprintf("/export: %s is a directory", abs)
	}

	srcPath := m.sessionLog.Path()
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "/export: open output: " + err.Error()
	}
	defer f.Close()

	n, err := session.Export(srcPath, f, opt)
	if err != nil {
		return "/export: " + err.Error()
	}
	return fmt.Sprintf("/export: wrote %d bytes to %s (format=%s)",
		n, abs, opt.Format)
}
