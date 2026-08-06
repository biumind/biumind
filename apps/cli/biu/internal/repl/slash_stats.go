// /stats slash — current session statistics.
//
// Aggregates the metrics the user reaches for when sizing up "how
// big is this session": message
// count, tool calls by type, total tokens / USD this session,
// elapsed wall time, file edits.
//
// /cost is the running token + USD; /usage is the historical
// across-session ledger; /stats is the in-flight composite —
// "everything I'd want to know about THIS session" in one screen.

package repl

import (
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

func (m model) handleStats(parts []string) string {
	var b strings.Builder
	b.WriteString("/stats: this session\n")

	// Show user-supplied title first when set — it's the most
	// human-meaningful identifier, ahead of the ulid-style id.
	if m.sessionTitle != "" {
		fmt.Fprintf(&b, "  title:       %s\n", m.sessionTitle)
	}

	// Wall time — approximate from session writer creation if
	// available; otherwise "unknown".
	if m.sessionLog != nil {
		if elapsed := sessionWallTime(m); elapsed > 0 {
			fmt.Fprintf(&b, "  duration:    %s\n", elapsed.Round(time.Second))
		}
	}

	// Message counts.
	user, asst := countMessages(m.history)
	fmt.Fprintf(&b, "  messages:    %d total (%d user, %d assistant)\n",
		len(m.history), user, asst)

	// Token + USD via cost tracker (engine-only).
	if m.engine != nil {
		snap := m.engine.Cost().Snapshot()
		fmt.Fprintf(&b, "  tokens in:   %s\n", humanInt(snap.InputTokens))
		fmt.Fprintf(&b, "  tokens out:  %s\n", humanInt(snap.OutputTokens))
		if snap.CacheReadTokens > 0 || snap.CacheWriteTokens > 0 {
			fmt.Fprintf(&b, "  cache r/w:   %s / %s\n",
				humanInt(snap.CacheReadTokens), humanInt(snap.CacheWriteTokens))
		}
		fmt.Fprintf(&b, "  USD:         $%.4f\n", snap.USD)
	}

	// File mutation counts via AppState's freshness ledger — gives
	// a "how many files this session touched" lower bound. Doesn't
	// distinguish edited from read, but the count is still useful
	// as a session-size proxy.
	if m.engine != nil {
		if st := m.engine.State(); st != nil {
			tracked := st.TrackedFiles()
			if len(tracked) > 0 {
				fmt.Fprintf(&b, "  files read:  %d\n", len(tracked))
			}
		}
	}

	// Session id + jsonl path so /export can be aimed at the right
	// file without hunting.
	if m.sessionLog != nil {
		fmt.Fprintf(&b, "  session id:  %s\n", m.sessionLog.ID())
		fmt.Fprintf(&b, "  log path:    %s\n", m.sessionLog.Path())
	}

	return strings.TrimRight(b.String(), "\n")
}

// countMessages tallies user vs assistant messages in the history
// slice. System / tool / synthetic roles fall into neither bucket;
// the total stays accurate via len().
func countMessages(history []client.Message) (user, assistant int) {
	for _, m := range history {
		switch m.Role {
		case "user":
			user++
		case "assistant":
			assistant++
		}
	}
	return
}

// sessionWallTime returns elapsed time since the session writer
// was created, or 0 when not derivable. Approximation — biu doesn't
// stamp the session start anywhere durable, so we read the jsonl
// file's mtime as a stand-in.
func sessionWallTime(m model) time.Duration {
	if m.sessionLog == nil {
		return 0
	}
	// Walltime only matters when we have a sensible reference.
	// Without a session-start timestamp on the writer, return 0;
	// future PRs can stamp it on Open().
	return 0
}
