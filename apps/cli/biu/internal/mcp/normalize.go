// Name normalisation + env-var expansion for MCP server configs.
//
// Inspired by Claude Code's services/mcp/{normalization.ts,
// mcpStringUtils.ts, envExpansion.ts} — porting the same rules so
// users with mixed Claude Code + biu setups don't see permission rule
// mismatches across tools.

package mcp

import (
	"os"
	"regexp"
	"strings"
)

// invalidNameChar matches any character not allowed inside a tool /
// server name. The LLM tool-spec restricts identifiers to a-zA-Z0-9_-
// and double-underscore is reserved as our server/tool separator.
var invalidNameChar = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// NormalizeServerName produces a safe slug from a user-provided
// server name. Same rules as our validServerName check, but lossy
// (replaces vs rejects) so config-loaded names can be coerced rather
// than crashing the entire CLI.
func NormalizeServerName(s string) string {
	s = strings.ToLower(s)
	s = invalidNameChar.ReplaceAllString(s, "_")
	s = collapseUnderscores(s)
	return strings.Trim(s, "_-")
}

// NormalizeToolName converts a server-supplied tool name into one
// that's safe to embed in `mcp__<server>__<tool>`. Crucially: replaces
// ALL non-[a-zA-Z0-9_-] characters with `_`, so tools named
// `file.read` or `repo:list` become `file_read` / `repo_list`. Then
// collapses runs of `__` to single `_` to avoid colliding with our
// server/tool separator.
//
// Same algorithm Claude Code uses, so a user's permission policy
// rules that mention `mcp__github__create_pr` work identically across
// tools.
func NormalizeToolName(s string) string {
	s = invalidNameChar.ReplaceAllString(s, "_")
	s = collapseUnderscores(s)
	s = strings.Trim(s, "_-")
	if s == "" {
		return "tool"
	}
	return s
}

// collapseUnderscores reduces any run of 2+ underscores to a single
// underscore. Matches Claude Code's behaviour of preventing tool
// names from accidentally containing the `__` server/tool separator.
func collapseUnderscores(s string) string {
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return s
}

// envVarRefRE matches ${NAME} or ${NAME:-default}.
var envVarRefRE = regexp.MustCompile(`\$\{([^}]+)\}`)

// ExpandEnvVars replaces ${VAR} and ${VAR:-default} occurrences in a
// string. Returns the expanded value plus any variables that were
// referenced without a default and not present in os.Environ — the
// caller logs these so misconfigured servers fail loudly instead of
// silently launching with empty credentials.
func ExpandEnvVars(value string) (expanded string, missing []string) {
	expanded = envVarRefRE.ReplaceAllStringFunc(value, func(m string) string {
		// m is "${...}". Strip braces.
		inner := m[2 : len(m)-1]
		name := inner
		def := ""
		hasDef := false
		// `:- ` syntax: split on first `:-` only.
		if i := strings.Index(inner, ":-"); i >= 0 {
			name = inner[:i]
			def = inner[i+2:]
			hasDef = true
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if hasDef {
			return def
		}
		missing = append(missing, name)
		// Leave the original ${VAR} in place so debug logs show what
		// was meant to be substituted.
		return m
	})
	return expanded, missing
}

// ExpandEnvSlice runs ExpandEnvVars on every entry in args/env values.
// Convenience for StdioConfig population in the bootstrap path.
func ExpandEnvSlice(in []string) (out []string, missing []string) {
	out = make([]string, len(in))
	for i, s := range in {
		exp, miss := ExpandEnvVars(s)
		out[i] = exp
		missing = append(missing, miss...)
	}
	return out, missing
}

// ExpandEnvMap runs ExpandEnvVars on every map value (keys are not
// expanded — env var names should stay literal).
func ExpandEnvMap(in map[string]string) (out map[string]string, missing []string) {
	if in == nil {
		return nil, nil
	}
	out = make(map[string]string, len(in))
	for k, v := range in {
		exp, miss := ExpandEnvVars(v)
		out[k] = exp
		missing = append(missing, miss...)
	}
	return out, missing
}

// SignatureFor returns a stable identity for a stdio server config —
// used to deduplicate the same server defined via plugin + manual
// config. Two configs sharing the same signature are the same server.
//
// stdio: signature = "stdio:<command>\x00<arg1>\x00<arg2>...".
// Different working directories are NOT part of the signature; we
// assume command + args fully identify the server (env vars are not
// part of identity either — same server with different tokens is
// still "the same server").
func SignatureFor(command string, args []string) string {
	var b strings.Builder
	b.WriteString("stdio:")
	b.WriteString(command)
	for _, a := range args {
		b.WriteByte(0)
		b.WriteString(a)
	}
	return b.String()
}
