// Micro compact: in-place fixups to a message slice that don't need
// an LLM round-trip:
//
//   * Dedupe stale Read results — when the model has called
//     Read(file_path=X) more than once, keep only the most recent
//     full-content result and replace the older ones with a one-line
//     `(superseded by later Read of X)` placeholder.
//   * Truncate over-long tool results (Bash output, Grep matches)
//     to a hard char cap so a single chatty command can't blow up
//     the context budget.
//
// Engine calls Apply before every provider.Stream so the savings
// compound over the lifetime of a turn loop.

package compact

import (
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// MicroOptions tunes the behaviour. Zero values keep things off.
type MicroOptions struct {
	// MaxToolResultChars caps each tool_result content block. Longer
	// outputs get truncated with a marker. 0 = no cap.
	MaxToolResultChars int

	// DedupeReads, when true, replaces all but the most recent
	// Read(file_path=X) result with a placeholder.
	DedupeReads bool
}

// Default returns sensible defaults: 8000 chars cap + dedupe on.
func Default() MicroOptions {
	return MicroOptions{
		MaxToolResultChars: 8000,
		DedupeReads:        true,
	}
}

// Apply rewrites msgs in place under the supplied options. Returns
// the number of bytes saved (approximate — char count delta).
func Apply(msgs []state.Message, opt MicroOptions) int {
	saved := 0
	if opt.DedupeReads {
		saved += dedupeReads(msgs)
	}
	if opt.MaxToolResultChars > 0 {
		saved += truncateLongResults(msgs, opt.MaxToolResultChars)
	}
	return saved
}

// dedupeReads scans the conversation for tool_use blocks named
// "Read" and the matching tool_result blocks. For each file_path
// that appears in more than one Read, every tool_result EXCEPT the
// most recent is replaced with a one-line placeholder.
func dedupeReads(msgs []state.Message) int {
	// First pass: build (use_id → file_path) for every Read
	// invocation. Track ordering so we know which is the latest.
	type readUse struct {
		useID string
		path  string
	}
	var reads []readUse
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type != state.ContentToolUse || b.ToolUseName != "Read" {
				continue
			}
			path, _ := b.ToolUseInput["file_path"].(string)
			if path == "" {
				path, _ = b.ToolUseInput["path"].(string)
			}
			if path == "" {
				continue
			}
			reads = append(reads, readUse{useID: b.ToolUseID, path: path})
		}
	}
	if len(reads) < 2 {
		return 0
	}
	// Latest read per path = the last useID we saw for it.
	latestByPath := map[string]string{}
	for _, r := range reads {
		latestByPath[r.path] = r.useID
	}
	// Stale set = every read whose useID isn't latestByPath[path].
	stale := map[string]string{} // useID → file_path
	for _, r := range reads {
		if latestByPath[r.path] == r.useID {
			continue
		}
		stale[r.useID] = r.path
	}
	if len(stale) == 0 {
		return 0
	}
	// Second pass: rewrite every tool_result whose ID is in stale.
	saved := 0
	for i := range msgs {
		for j := range msgs[i].Content {
			b := &msgs[i].Content[j]
			if b.Type != state.ContentToolResult {
				continue
			}
			path, ok := stale[b.ToolResultID]
			if !ok {
				continue
			}
			before := contentLength(b.ToolResultContent)
			b.ToolResultContent = []state.ContentBlock{{
				Type: state.ContentText,
				Text: fmt.Sprintf("(superseded by later Read of %s)", path),
			}}
			after := contentLength(b.ToolResultContent)
			saved += before - after
		}
	}
	return saved
}

// truncateLongResults caps every tool_result text block at maxChars,
// keeping the head + tail and inserting a marker.
func truncateLongResults(msgs []state.Message, maxChars int) int {
	saved := 0
	for i := range msgs {
		for j := range msgs[i].Content {
			b := &msgs[i].Content[j]
			if b.Type != state.ContentToolResult {
				continue
			}
			for k := range b.ToolResultContent {
				inner := &b.ToolResultContent[k]
				if inner.Type != state.ContentText {
					continue
				}
				if len(inner.Text) <= maxChars {
					continue
				}
				before := len(inner.Text)
				head := maxChars / 2
				tail := maxChars - head - 32 // leave room for marker
				if tail < 0 {
					tail = 0
				}
				inner.Text = inner.Text[:head] +
					fmt.Sprintf("\n…(truncated %d bytes)…\n",
						before-head-tail) +
					inner.Text[before-tail:]
				saved += before - len(inner.Text)
			}
		}
	}
	_ = strings.Builder{} // avoid unused-import warning if strings goes away
	return saved
}

func contentLength(blocks []state.ContentBlock) int {
	n := 0
	for _, b := range blocks {
		n += len(b.Text)
	}
	return n
}
