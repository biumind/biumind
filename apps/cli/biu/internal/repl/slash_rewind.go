// /rewind slash — list + apply file-state snapshots captured by the
// engine across the current session.
//
// biu's snapshot infrastructure (internal/session/snapshots.go) already captures pre-Edit /
// pre-Write file bytes keyed off the user-message UUID; the CLI
// flag --rewind-files exposes a one-shot restore. This slash
// brings the same capability into the live REPL so users can:
//
//   /rewind                      — list every captured snapshot
//                                  with its uuid + first-line
//                                  prompt + affected file count
//   /rewind <uuid>               — restore every file changed AFTER
//                                  the given message uuid back to
//                                  its pre-message bytes; reports
//                                  what was restored
//   /rewind <uuid> --dry-run     — preview without writing
//
// The slash is read-only on the SESSION (history stays); only the
// filesystem is mutated. Pair with `/clear` if you also want to
// drop the conversation history.

package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// handleRewind dispatches /rewind and its sub-forms.
func (m model) handleRewind(parts []string) string {
	store, err := m.openSnapshotStore()
	if err != nil {
		return "/rewind: " + err.Error()
	}
	if store == nil {
		return "/rewind: snapshot store not available — " +
			"is this a fresh session? snapshots are captured " +
			"per Edit/Write tool call against the active session id"
	}

	if len(parts) <= 1 {
		return renderRewindList(store)
	}

	target := parts[1]
	dryRun := false
	for _, p := range parts[2:] {
		if p == "--dry-run" || p == "-n" {
			dryRun = true
		}
	}

	return runRewind(store, target, dryRun)
}

// openSnapshotStore returns the store for the live session id, or
// nil + nil error when snapshots aren't applicable in this REPL
// (chat-only mode, no session writer, etc).
func (m model) openSnapshotStore() (*session.SnapshotStore, error) {
	if m.sessionLog == nil {
		return nil, nil
	}
	id := m.sessionLog.ID()
	if id == "" {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	return session.NewSnapshotStore(home, id)
}

// renderRewindList shows every captured snapshot uuid + the count
// of distinct files inside. Most-recent first so users scrolling
// up see the latest snapshot at the top.
func renderRewindList(store *session.SnapshotStore) string {
	entries, err := store.Entries()
	if err != nil {
		return "/rewind: read index: " + err.Error()
	}
	if len(entries) == 0 {
		return "/rewind: no snapshots captured this session yet. " +
			"Snapshots fire on Edit / Write / NotebookEdit; do a " +
			"file mutation then re-run /rewind."
	}
	// Group by uuid — one user message can produce many file
	// snapshots (multi-file Edit batch).
	type group struct {
		UUID  string
		Files []string
	}
	byUUID := map[string]*group{}
	var order []string
	for _, e := range entries {
		g, ok := byUUID[e.UUID]
		if !ok {
			g = &group{UUID: e.UUID}
			byUUID[e.UUID] = g
			order = append(order, e.UUID)
		}
		g.Files = append(g.Files, e.Path)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "/rewind: %d snapshot uuid(s) — most-recent first:\n", len(order))
	// Iterate in reverse for most-recent first.
	for i := len(order) - 1; i >= 0; i-- {
		uid := order[i]
		g := byUUID[uid]
		fmt.Fprintf(&b, "  %s — %d file%s",
			uid, len(g.Files), pluralS(len(g.Files)))
		if len(g.Files) > 0 {
			preview := g.Files[0]
			if len(g.Files) > 1 {
				preview = fmt.Sprintf("%s, +%d more", preview, len(g.Files)-1)
			}
			fmt.Fprintf(&b, "  (%s)", preview)
		}
		b.WriteByte('\n')
	}
	b.WriteString("\nrestore: /rewind <uuid> [--dry-run]")
	return b.String()
}

// runRewind applies the snapshot identified by uuid. dryRun=true
// reports what would be touched without writing.
func runRewind(store *session.SnapshotStore, uuid string, dryRun bool) string {
	hit, err := store.HasUUID(uuid)
	if err != nil {
		return "/rewind: scan: " + err.Error()
	}
	if !hit {
		return fmt.Sprintf("/rewind: uuid %q not found in this session's snapshots. "+
			"Run /rewind without args to list available uuids.", uuid)
	}
	if dryRun {
		// We can't easily preview without restoring; surface what
		// we know without mutating. The store doesn't ship a
		// dry-run primitive, so we list the affected files
		// (already known via Entries) and stop short of writing.
		entries, _ := store.Entries()
		var matched []string
		for _, e := range entries {
			if e.UUID == uuid {
				matched = append(matched, e.Path)
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "/rewind --dry-run %s — would restore %d file(s):\n",
			uuid, len(matched))
		for _, p := range matched {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		b.WriteString("\nrun without --dry-run to apply.")
		return b.String()
	}
	count, paths, err := store.Rewind(uuid)
	if err != nil {
		return "/rewind: " + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/rewind %s — restored %d file(s):\n", uuid, count)
	for _, p := range paths {
		fmt.Fprintf(&b, "  ✓ %s\n", p)
	}
	b.WriteString("\nfiles are back to pre-message state. " +
		"The conversation history is unchanged — use /clear to also drop history.")
	return strings.TrimRight(b.String(), "\n")
}
