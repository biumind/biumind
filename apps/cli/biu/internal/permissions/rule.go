// Permission rule parsing + tool/command matching.
//
// Wire format follows settings.json:
//
//   {
//     "permissions": {
//       "allow": ["Bash(npm install)", "Edit(/repo/**)", "Read"],
//       "deny":  ["Bash(rm:*)", "Bash(curl https://*)"],
//       "ask":   ["Bash(git push:*)"]
//     }
//   }
//
// Rule string grammar:
//
//   tool                 — entire tool, no content
//   tool(content)        — tool + specific content (path, command, …)
//   tool(prefix:*)       — legacy prefix match: matches `prefix` + `prefix args`
//   tool(pattern*)       — wildcard: * matches any chars
//   tool(\(literal\))    — escaped parens in content
//
// Matching semantics:
//
//   * Tool-only rule (no content) matches the entire tool regardless of args.
//   * Tool(content) matches when:
//       Bash: command exact / prefix / wildcard match against
//             input["command"]
//       Edit/Write/Read/Glob/Grep: filepath.Match against input["path"] / pattern.
//             "**" wildcard supported (regex-style).
//       Other tools: exact match against the most-meaningful arg (auto-detected).
//
// Legacy biu rules `tool:detail` are also accepted (treated as
// `tool(detail)`) so older config keeps working.

package permissions

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Behavior is what kind of decision a rule produces.
type Behavior string

const (
	BehaviorAllow Behavior = "allow"
	BehaviorDeny  Behavior = "deny"
	BehaviorAsk   Behavior = "ask"
)

// Source identifies where a rule came from. Useful for explaining a
// decision in the UI.
type Source string

const (
	SrcUserSettings    Source = "userSettings"
	SrcProjectSettings Source = "projectSettings"
	SrcLocalSettings   Source = "localSettings"
	SrcFlagSettings    Source = "flagSettings"
	SrcCLIArg          Source = "cliArg"
	SrcCommand         Source = "command"
	SrcSession         Source = "session"
)

// RuleValue is the parsed (toolName, optional content) tuple.
type RuleValue struct {
	ToolName    string
	RuleContent string // empty when the rule covers the entire tool
}

// Rule is a parsed rule + its provenance + behaviour.
type Rule struct {
	Source   Source
	Behavior Behavior
	Value    RuleValue
}

// ParseRuleString parses one settings.json rule string into a Value.
// Accepts both paren syntax (`Bash(npm install)`) and legacy biu syntax
// (`bash:npm install`). Unknown / malformed strings produce a value
// with the whole input as the toolName, matching the permissive
// behaviour.
func ParseRuleString(s string) RuleValue {
	s = strings.TrimSpace(s)
	if s == "" {
		return RuleValue{}
	}

	// Paren syntax first.
	open := findFirstUnescapedChar(s, '(')
	if open >= 0 {
		closeIdx := findLastUnescapedChar(s, ')')
		if closeIdx > open && closeIdx == len(s)-1 {
			tool := s[:open]
			raw := s[open+1 : closeIdx]
			if tool != "" {
				if raw == "" || raw == "*" {
					return RuleValue{ToolName: tool}
				}
				return RuleValue{ToolName: tool, RuleContent: unescapeRuleContent(raw)}
			}
		}
	}

	// Legacy biu colon syntax: `tool:detail`. Legacy callers expected
	// prefix-match semantics (so `bash:ls` matched both `ls` and
	// `ls -la`). Preserve that by promoting the detail to the
	// prefix form `detail:*` when it doesn't already encode wildcard
	// or prefix syntax.
	if i := strings.Index(s, ":"); i > 0 {
		tool := s[:i]
		detail := s[i+1:]
		if detail == "" || detail == "*" || detail == "**" {
			return RuleValue{ToolName: tool}
		}
		if !strings.HasSuffix(detail, ":*") && !hasUnescapedStar(detail) {
			detail = detail + ":*"
		}
		return RuleValue{ToolName: tool, RuleContent: detail}
	}

	return RuleValue{ToolName: s}
}

// String formats a RuleValue back to wire form.
func (v RuleValue) String() string {
	if v.RuleContent == "" {
		return v.ToolName
	}
	return fmt.Sprintf("%s(%s)", v.ToolName, escapeRuleContent(v.RuleContent))
}

// MatchTool returns true when this rule covers the given (toolName,
// input) call. Rule-content semantics depend on the tool family —
// shell-style commands for Bash, glob for path tools, exact match
// otherwise.
func (v RuleValue) MatchTool(toolName string, input map[string]any) bool {
	if !sameToolName(v.ToolName, toolName) {
		return false
	}
	if v.RuleContent == "" {
		// Tool-only rule matches every invocation.
		return true
	}

	switch toolName {
	case "Bash", "bash":
		cmd, _ := input["command"].(string)
		if cmd == "" {
			cmd, _ = input["cmd"].(string)
		}
		return matchShellPattern(v.RuleContent, cmd)
	case "Edit", "edit", "Write", "write", "Read", "read", "Glob", "glob":
		// Path-style tools: match against `path`/`file_path`/`pattern`.
		target := stringField(input, "path", "file_path", "pattern")
		return matchPathPattern(v.RuleContent, target)
	case "Grep", "grep":
		target := stringField(input, "pattern", "path")
		return matchPathPattern(v.RuleContent, target)
	}

	// Default: try the path matcher first, then fall back to exact
	// match against any string field that looks plausible.
	if target := stringField(input, "path", "file_path", "pattern", "command", "url", "query"); target != "" {
		return matchPathPattern(v.RuleContent, target)
	}
	return false
}

// sameToolName allows case-insensitive comparison so rules written as
// "bash" match the engine's "Bash" tool, and vice versa. MCP-prefixed
// tools (mcp__server__name) also match the rule mcp__server (server-
// level allow).
func sameToolName(rule, actual string) bool {
	if strings.EqualFold(rule, actual) {
		return true
	}
	// MCP server-level rule matches any tool from that server.
	if strings.HasPrefix(actual, "mcp__") && strings.HasPrefix(rule, "mcp__") {
		// rule "mcp__srv" ⇒ matches "mcp__srv__anything".
		// rule "mcp__srv__*" ⇒ same.
		base := strings.TrimSuffix(rule, "__*")
		base = strings.TrimSuffix(base, "*")
		return strings.HasPrefix(actual, base+"__") || actual == base
	}
	return false
}

// stringField returns the first non-empty string-typed value found
// under one of the candidate keys.
func stringField(input map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := input[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// ─── Shell command matching ────────────────────────────

// matchShellPattern handles three rule shapes:
//
//	exact:    "npm install"  → matches `npm install` only
//	prefix:   "git:*"        → matches "git" or "git anything"
//	wildcard: "rm * /tmp/*"  → glob-style wildcard matching
//
// Empty rule content / "*" never reaches here (filtered upstream); it
// would match-all if it did.
func matchShellPattern(rule, command string) bool {
	rule = strings.TrimSpace(rule)
	command = strings.TrimSpace(command)
	if rule == "" {
		return true
	}
	// Legacy prefix syntax `prefix:*`.
	if strings.HasSuffix(rule, ":*") {
		prefix := strings.TrimSuffix(rule, ":*")
		return command == prefix || strings.HasPrefix(command, prefix+" ")
	}
	// Wildcard syntax (contains `*` not at :* end).
	if hasUnescapedStar(rule) {
		return matchWildcard(rule, command)
	}
	return command == rule
}

// matchWildcard converts a glob-ish pattern to a regex anchored on
// both ends. * → .*, escaped \* → literal *. Trailing ` *` (space +
// wildcard) is treated as optional (so `git *` matches both `git add`
// and bare `git`).
func matchWildcard(pattern, target string) bool {
	const starPH = "\x00ESC_STAR\x00"
	const bsPH = "\x00ESC_BS\x00"
	// Replace escape sequences with placeholders.
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			next := pattern[i+1]
			switch next {
			case '*':
				b.WriteString(starPH)
				i++
				continue
			case '\\':
				b.WriteString(bsPH)
				i++
				continue
			}
		}
		b.WriteByte(pattern[i])
	}
	processed := b.String()
	// Quote regex specials except '*'.
	escaped := regexp.QuoteMeta(processed)
	// QuoteMeta escapes `*` too — undo it.
	escaped = strings.ReplaceAll(escaped, `\*`, `*`)
	// Convert `*` to `.*`.
	regexPat := strings.ReplaceAll(escaped, "*", ".*")
	// Restore escaped-literal placeholders.
	regexPat = strings.ReplaceAll(regexPat, starPH, `\*`)
	regexPat = strings.ReplaceAll(regexPat, bsPH, `\\`)

	// Trailing-wildcard optional-args trick.
	if strings.HasSuffix(regexPat, " .*") &&
		strings.Count(processed, "*") == 1 {
		regexPat = strings.TrimSuffix(regexPat, " .*") + "( .*)?"
	}
	re, err := regexp.Compile("(?s)^" + regexPat + "$")
	if err != nil {
		return false
	}
	return re.MatchString(target)
}

// matchPathPattern matches a glob against a path. Supports `**`
// (any-depth) by translating to a regex; `*` and `?` go through
// filepath.Match.
func matchPathPattern(pattern, target string) bool {
	if pattern == target {
		return true
	}
	if strings.Contains(pattern, "**") {
		regexPat := regexp.QuoteMeta(pattern)
		regexPat = strings.ReplaceAll(regexPat, `\*\*`, `.*`)
		regexPat = strings.ReplaceAll(regexPat, `\*`, `[^/]*`)
		regexPat = strings.ReplaceAll(regexPat, `\?`, `.`)
		if re, err := regexp.Compile("^" + regexPat + "$"); err == nil {
			return re.MatchString(target)
		}
		return false
	}
	if ok, _ := filepath.Match(pattern, target); ok {
		return true
	}
	return strings.HasPrefix(target, pattern)
}

// ─── Escape helpers ───────────────────────────────────

func escapeRuleContent(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `(`, `\(`)
	s = strings.ReplaceAll(s, `)`, `\)`)
	return s
}

func unescapeRuleContent(s string) string {
	s = strings.ReplaceAll(s, `\(`, `(`)
	s = strings.ReplaceAll(s, `\)`, `)`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

func findFirstUnescapedChar(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			continue
		}
		bs := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 {
			return i
		}
	}
	return -1
}

func findLastUnescapedChar(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != c {
			continue
		}
		bs := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 {
			return i
		}
	}
	return -1
}

func hasUnescapedStar(s string) bool {
	if strings.HasSuffix(s, ":*") {
		return false // legacy prefix
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '*' {
			continue
		}
		bs := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 {
			return true
		}
	}
	return false
}
