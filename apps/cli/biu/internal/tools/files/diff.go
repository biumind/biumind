// Tiny unified-diff helper used by Edit/Write/MultiEdit to surface
// the change set in tool results. The TUI renders these `+` / `-`
// lines in green/red so the user sees exactly what the model
// changed.
//
// We deliberately do NOT pull a heavy diff library — Edit + Write
// only need a short summary, not a full Myers diff. The algorithm is
// the LCS-based row diff from sergi/go-diff trimmed to the bits we
// need; in practice ~50 lines does the job.

package files

import (
	"fmt"
	"strings"
)

// UnifiedDiff builds a compact unified-diff hunk between `before` and
// `after`. `path` is the filename used in the header. context is the
// number of unchanged lines kept around each change (0 = changes only).
//
// Output shape:
//
//	--- a/<path>
//	+++ b/<path>
//	@@ -<oldStart>,<oldLen> +<newStart>,<newLen> @@
//	 unchanged context line
//	-removed line
//	+added line
//
// Empty when before == after.
func UnifiedDiff(path, before, after string, context int) string {
	if before == after {
		return ""
	}
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")
	hunks := diffHunks(a, b, context)
	if len(hunks) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n", path)
	fmt.Fprintf(&out, "+++ b/%s\n", path)
	for _, h := range hunks {
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n",
			h.oldStart+1, h.oldLen, h.newStart+1, h.newLen)
		for _, line := range h.lines {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return strings.TrimRight(out.String(), "\n")
}

type hunk struct {
	oldStart, oldLen int
	newStart, newLen int
	lines            []string
}

// diffHunks runs an LCS-based diff and groups the resulting edit
// script into hunks with `context` unchanged lines on each side.
func diffHunks(a, b []string, context int) []hunk {
	ops := lcsDiff(a, b)
	if len(ops) == 0 {
		return nil
	}

	// Walk ops, emitting hunks. We accumulate prefix context, switch
	// to "in hunk" when we hit a non-equal op, and close the hunk
	// when we've seen `2*context` consecutive equals.
	var hunks []hunk
	var current *hunk
	pendingEquals := 0
	closeHunk := func(aIdx, bIdx int) {
		if current == nil {
			return
		}
		hunks = append(hunks, *current)
		current = nil
		pendingEquals = 0
	}

	aIdx, bIdx := 0, 0
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			if current != nil {
				current.lines = append(current.lines, " "+op.line)
				current.oldLen++
				current.newLen++
				pendingEquals++
				if pendingEquals >= context*2 && context > 0 {
					// Trim trailing equals to leave only `context`
					// lines, then close.
					excess := pendingEquals - context
					trim := len(current.lines) - excess
					trimmed := current.lines[:trim]
					current.lines = trimmed
					current.oldLen -= excess
					current.newLen -= excess
					closeHunk(aIdx, bIdx)
				}
			}
			aIdx++
			bIdx++
		case opDel:
			if current == nil {
				current = &hunk{
					oldStart: maxInt(0, aIdx-context),
					newStart: maxInt(0, bIdx-context),
				}
				// Pre-pull preceding context.
				for i := current.oldStart; i < aIdx; i++ {
					current.lines = append(current.lines, " "+a[i])
					current.oldLen++
					current.newLen++
				}
			}
			current.lines = append(current.lines, "-"+op.line)
			current.oldLen++
			pendingEquals = 0
			aIdx++
		case opAdd:
			if current == nil {
				current = &hunk{
					oldStart: maxInt(0, aIdx-context),
					newStart: maxInt(0, bIdx-context),
				}
				for i := current.newStart; i < bIdx; i++ {
					current.lines = append(current.lines, " "+b[i])
					current.oldLen++
					current.newLen++
				}
			}
			current.lines = append(current.lines, "+"+op.line)
			current.newLen++
			pendingEquals = 0
			bIdx++
		}
	}
	if current != nil {
		hunks = append(hunks, *current)
	}
	return hunks
}

type diffOp struct {
	kind int
	line string
}

const (
	opEqual = iota
	opDel
	opAdd
)

// lcsDiff returns an edit script transforming a → b using the
// classic dynamic-programming LCS table. O(N*M) in space — fine for
// the file sizes Edit/Write actually deal with (capped at 8MB by
// Read).
func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// table[i][j] = LCS length for a[:i] vs b[:j]
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				table[i][j] = table[i-1][j-1] + 1
			} else if table[i-1][j] >= table[i][j-1] {
				table[i][j] = table[i-1][j]
			} else {
				table[i][j] = table[i][j-1]
			}
		}
	}
	// Backtrack to recover the edit script.
	var ops []diffOp
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			ops = append([]diffOp{{kind: opEqual, line: a[i-1]}}, ops...)
			i--
			j--
		case j > 0 && (i == 0 || table[i][j-1] >= table[i-1][j]):
			ops = append([]diffOp{{kind: opAdd, line: b[j-1]}}, ops...)
			j--
		default:
			ops = append([]diffOp{{kind: opDel, line: a[i-1]}}, ops...)
			i--
		}
	}
	return ops
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
