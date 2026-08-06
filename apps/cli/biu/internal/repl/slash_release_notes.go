// /release-notes slash — show the embedded biu RELEASE_NOTES.md.
//
// The file is embedded at build time so a fresh `go install` always carries the matching
// notes — users running an older binary see THEIR version's notes,
// not whatever is in upstream main today.
//
// Subcommands:
//
//	/release-notes              — show the most recent N lines
//	/release-notes full         — show the entire file
//	/release-notes <substring>  — grep notes for a term

package repl

import (
	_ "embed"
	"fmt"
	"strings"
)

// releaseNotesBody is the canonical notes file. Embedded at compile
// time so the slash needs no filesystem access.
//
// The .md file is a build-time copy of apps/cli/biu/RELEASE_NOTES.md
// — refresh it before tagging a release. Symlinks would be cleaner
// but go:embed rejects them ("cannot embed irregular file"), so we
// keep an actual copy. Drift between the canonical and the embed
// is caught by the release-notes test which fails on a hash
// mismatch (see slash_release_notes_test.go).
//
//go:embed release_notes_embed.md
var releaseNotesBody string

// DefaultReleaseNotesTail is how many lines /release-notes shows
// without `full` — the most-recent slice that fits on a typical
// terminal scroll buffer.
const DefaultReleaseNotesTail = 80

func (m model) handleReleaseNotes(parts []string) string {
	body := strings.TrimSpace(releaseNotesBody)
	if body == "" {
		return "/release-notes: notes not embedded in this build"
	}

	if len(parts) > 1 {
		switch parts[1] {
		case "full":
			return body
		default:
			// Treat as substring filter — case-insensitive grep.
			needle := strings.ToLower(strings.Join(parts[1:], " "))
			return grepReleaseNotes(body, needle)
		}
	}

	// Default: tail N lines.
	lines := strings.Split(body, "\n")
	start := 0
	if len(lines) > DefaultReleaseNotesTail {
		start = len(lines) - DefaultReleaseNotesTail
	}
	hint := ""
	if start > 0 {
		hint = fmt.Sprintf("\n\n(showing last %d of %d lines — `/release-notes full` for the rest)",
			DefaultReleaseNotesTail, len(lines))
	}
	return strings.Join(lines[start:], "\n") + hint
}

// grepReleaseNotes returns lines matching needle (already lower-
// cased) along with one line of context above and below for
// readability — without context, single-line matches in a structured
// notes file (`#### TaskOutput / TaskStop tools — P20.27`) lose
// their anchoring.
func grepReleaseNotes(body, needle string) string {
	if needle == "" {
		return "/release-notes: filter is empty"
	}
	lines := strings.Split(body, "\n")
	hits := map[int]bool{}
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), needle) {
			hits[i] = true
		}
	}
	if len(hits) == 0 {
		return fmt.Sprintf("/release-notes: no matches for %q", needle)
	}
	// Expand each hit by ±1 line of context, then dedupe via set.
	keep := map[int]bool{}
	for i := range hits {
		for d := -1; d <= 1; d++ {
			j := i + d
			if j >= 0 && j < len(lines) {
				keep[j] = true
			}
		}
	}
	var out []string
	prev := -2
	for i := 0; i < len(lines); i++ {
		if !keep[i] {
			continue
		}
		// Insert "---" between non-contiguous matches so users see
		// distinct hit groups.
		if i > prev+1 && len(out) > 0 {
			out = append(out, "---")
		}
		marker := "  "
		if hits[i] {
			marker = "» "
		}
		out = append(out, marker+lines[i])
		prev = i
	}
	return fmt.Sprintf("/release-notes: %d match(es) for %q\n\n%s",
		len(hits), needle, strings.Join(out, "\n"))
}
