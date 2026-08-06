// Command-aware exit-code interpretation. Many shell tools use exit
// codes for non-error signals — `grep` returns 1 when no match,
// `diff` returns 1 when files differ, `test`/`[` returns 1 when the
// condition is false. Treating any of those as "the bash tool
// failed" sends the model down a debugging rabbit hole that doesn't
// exist.
//
// Mirrors a last-segment heuristic for compound pipelines: sh's $?
// reports the last pipeline component's status, so the final segment
// is what determines success or failure.
//
// Heuristic, not a parser: the segmenter ignores shell-control
// operators inside quotes/backslash escapes but doesn't pretend to
// be a POSIX shell. Wrong-but-safe outcome is "fall back to default
// semantics" — the model still sees stderr + exit code in the
// payload and can investigate.

package web

import (
	"strings"
)

// commandSemantic decides whether (exitCode, stdout, stderr) means
// the command actually failed, plus an optional human-readable note
// surfaced alongside the result body.
type commandSemantic func(exitCode int, stdout, stderr string) (isError bool, message string)

// defaultSemantic: zero is success, anything else is failure. Used
// for any command not in the special-cases table.
func defaultSemantic(exitCode int, _, _ string) (bool, string) {
	if exitCode == 0 {
		return false, ""
	}
	return true, ""
}

// commandSemantics maps a base-command name to a result interpreter.
// Keep this table tight — every entry is a known divergence between
// "the process exited non-zero" and "something is actually wrong."
//
// Each entry tracks a known exit-code contract so the IsError flag
// the model sees stays consistent regardless of which prompt set it
// was trained against.
var commandSemantics = map[string]commandSemantic{
	// grep: 0=matches, 1=no matches (not an error), 2+=error.
	"grep": grepLikeSemantic,
	// ripgrep mirrors grep's exit codes.
	"rg": grepLikeSemantic,
	// find: 0=ok, 1=partial (some dirs unreadable), 2+=hard error.
	"find": func(exitCode int, _, _ string) (bool, string) {
		if exitCode <= 1 {
			msg := ""
			if exitCode == 1 {
				msg = "Some directories were inaccessible"
			}
			return false, msg
		}
		return true, ""
	},
	// diff: 0=identical, 1=differences (expected!), 2+=error.
	"diff": func(exitCode int, _, _ string) (bool, string) {
		if exitCode <= 1 {
			msg := ""
			if exitCode == 1 {
				msg = "Files differ"
			}
			return false, msg
		}
		return true, ""
	},
	// test: 0=true, 1=false (just a boolean), 2+=error.
	"test": testLikeSemantic,
	// `[` is the alias for test in shells.
	"[": testLikeSemantic,
}

func grepLikeSemantic(exitCode int, _, _ string) (bool, string) {
	if exitCode == 0 {
		return false, ""
	}
	if exitCode == 1 {
		return false, "No matches found"
	}
	return true, ""
}

func testLikeSemantic(exitCode int, _, _ string) (bool, string) {
	if exitCode == 0 {
		return false, ""
	}
	if exitCode == 1 {
		return false, "Condition is false"
	}
	return true, ""
}

// interpretCommandResult is the entry point: takes the user's
// command line plus the captured exit/stdout/stderr and returns
// (isError, optional note). Default semantics apply for any command
// not in the special-cases table.
func interpretCommandResult(command string, exitCode int, stdout, stderr string) (bool, string) {
	base := heuristicBaseCommand(command)
	sem := commandSemantics[base]
	if sem == nil {
		sem = defaultSemantic
	}
	return sem(exitCode, stdout, stderr)
}

// heuristicBaseCommand pulls the "interesting" command name from a
// pipeline. Shells set $? to the last pipeline element's status by
// default, so `ls | grep foo` should be interpreted with grep's
// semantics.
//
// Splits on top-level pipe / && / || / ; operators (skipping
// occurrences inside single-quoted, double-quoted, or
// backslash-escaped regions) and takes the last segment. Within
// that segment the leading whitespace-delimited token is the
// command. Returns "" when extraction fails — defaultSemantic then
// applies.
func heuristicBaseCommand(command string) string {
	segs := splitTopLevelSegments(command)
	if len(segs) == 0 {
		return ""
	}
	last := strings.TrimSpace(segs[len(segs)-1])
	if last == "" {
		return ""
	}
	// First whitespace-separated token. Ignore leading env-var
	// assignments like `FOO=bar grep ...` by stepping past tokens
	// that contain '=' before any whitespace.
	for {
		idx := indexAnyWhitespace(last)
		if idx < 0 {
			return last
		}
		head := last[:idx]
		if eq := strings.IndexByte(head, '='); eq < 0 {
			return head
		}
		last = strings.TrimLeft(last[idx:], " \t")
		if last == "" {
			return ""
		}
	}
}

// indexAnyWhitespace returns the first index of a space or tab; -1
// when neither appears. Tab matters because shell scripts sometimes
// use it as a separator.
func indexAnyWhitespace(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return i
		}
	}
	return -1
}

// splitTopLevelSegments breaks `command` on top-level shell-control
// operators (|, ||, &&, ;). Quote- and backslash-aware so segments
// inside `'...'`, `"..."`, or `\<x>` aren't split. Doesn't try to
// parse subshells / heredocs — those would push us into "real
// shell parser" territory; the heuristic is intentionally shallow.
func splitTopLevelSegments(s string) []string {
	var (
		segs       []string
		buf        strings.Builder
		inSingle   bool
		inDouble   bool
		escapeNext bool
	)
	flush := func() {
		segs = append(segs, buf.String())
		buf.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escapeNext {
			buf.WriteByte(c)
			escapeNext = false
			continue
		}
		switch {
		case c == '\\' && !inSingle:
			// Backslash escapes the next byte (outside single quotes).
			// We keep both bytes so the segment text remains a faithful
			// substring — the base-command extractor only looks at the
			// first token anyway.
			buf.WriteByte(c)
			escapeNext = true
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			buf.WriteByte(c)
		case c == '"' && !inSingle:
			inDouble = !inDouble
			buf.WriteByte(c)
		case inSingle || inDouble:
			buf.WriteByte(c)
		case c == ';':
			flush()
		case c == '|':
			// Both `|` and `||` end a segment — the next command's
			// exit code is what shell uses.
			if i+1 < len(s) && s[i+1] == '|' {
				i++
			}
			flush()
		case c == '&':
			// `&&` breaks the segment; lone `&` (background) is
			// rare in agent calls — treat it as a separator too so
			// the base-command extractor still sees the right tail.
			if i+1 < len(s) && s[i+1] == '&' {
				i++
			}
			flush()
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return segs
}
