// /rename slash — give the current session a human-readable title.
//
// Sessions on disk are named by timestamp + random suffix
// (20260529-073104-ab12cd34.jsonl) which is stable but unreadable
// in /resume pickers. /rename writes a "session_renamed" event into
// the JSONL so the title travels with the session and survives
// /resume; the model also caches it for the in-flight /stats line.
//
// Why an event instead of a sidecar file or a real fs rename?
//
//   - rename(2) on the live JSONL would invalidate Path() callers
//     mid-session (notably /share, /export, /stats).
//   - a sidecar adds a second file to keep in sync — JSONL events
//     already have replay semantics, so adding one more event type
//     is the cheapest path.
//   - last-event-wins lets you /rename multiple times; tooling that
//     reads titles just scans for the most recent session_renamed.
package repl

import (
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// handleRename parses /rename + sets the title. Returns the new
// model and a system note for the REPL.
//
// Forms:
//
//	/rename                   — show current title
//	/rename <title>           — set title to "<title>"
//	/rename clear             — drop the title (sets to "")
func (m model) handleRename(parts []string) (model, string) {
	if len(parts) < 2 {
		if m.sessionTitle == "" {
			return m, "/rename: no title set (try `/rename <title>`)"
		}
		return m, "/rename: current title = " + m.sessionTitle
	}
	rest := strings.TrimSpace(strings.TrimPrefix(strings.Join(parts[1:], " "), ""))
	// "clear" is a sentinel for unsetting — we accept it because a
	// bare /rename "" doesn't survive shell quoting.
	if strings.EqualFold(rest, "clear") {
		m.sessionTitle = ""
		appendRenameEvent(m.sessionLog, "")
		return m, "/rename: title cleared"
	}
	if rest == "" {
		return m, "/rename: empty title (use `/rename clear` to unset)"
	}
	if len(rest) > 200 {
		// Cap to keep /resume picker output sane. 200 chars is comfortably
		// over a normal terminal-width title.
		rest = rest[:200]
	}
	m.sessionTitle = rest
	appendRenameEvent(m.sessionLog, rest)
	return m, "/rename: title = " + rest
}

// appendRenameEvent writes the title-change event to the session
// log. Best-effort — a failed write doesn't surface to the user; the
// in-memory title is still applied so the session keeps its label
// for the rest of the run.
func appendRenameEvent(w *session.Writer, title string) {
	if w == nil {
		return
	}
	_ = w.Append(session.Event{
		Type:    "session_renamed",
		Content: title,
	})
}
