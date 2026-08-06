// Pure formatting helpers shared by status bar / slash handlers /
// system notes. All stateless — no model dependencies — so they
// live in their own file and slash handlers can import them
// without dragging in the rest of model.go.

package repl

import (
	"fmt"
	"strings"
)

// oneLineSlash flattens a multi-line description to a single line for
// `/agents` and similar listings. Caps to ~120 chars so the panel
// doesn't wrap unpredictably.
func oneLineSlash(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

// redactID truncates an install ID for display so the full
// machine-identifying token isn't echoed into a system message
// (which might be screen-shared / logged). Keeps the first/last
// 4 chars so the user can verify which install is which.
func redactID(id string) string {
	if id == "" {
		return "(none)"
	}
	if len(id) <= 8 {
		return id
	}
	return id[:4] + "…" + id[len(id)-4:]
}

// formatBytes renders a byte count with a unit. We don't pull in a
// humanize library; the table only needs KB / MB / GB precision.
func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.2f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.2f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1024*1024*1024))
	}
}

// truncateLine caps `s` at n runes (not bytes — multi-byte chars
// don't get split). Adds an ellipsis to make truncation visible.
func truncateLine(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// formatThousands inserts thousands separators into an int — easier
// to scan "12,345,678" than "12345678" when comparing token counts.
// We don't pull in a localisation package; the comma-thousand
// grouping is the convention everywhere biu's English UI surfaces.
func formatThousands(n int) string {
	if n < 0 {
		return "-" + formatThousands(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return formatThousands(n/1000) + fmt.Sprintf(",%03d", n%1000)
}

// formatPercent renders a 0..1 ratio as "NN%". Returns "n/a" when
// the input is out of range (caller hasn't measured yet) so a row
// doesn't show a misleading "0%".
func formatPercent(ratio float64) string {
	if ratio < 0 || ratio > 1 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", ratio*100)
}
