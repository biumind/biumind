// .env.example → config form schema. The analyze flow tries
// .env.example / .env.sample / .env.template in order; when none exists
// the repo simply has no declarable config (nil schema, no error).
//
// Line grammar handled: blank lines, full-line comments, `export KEY=`,
// quoted values ('...' / "..."), inline ` # comment` tails. The comment
// becomes the field label shown on the confirm page.

package repoanalyze

import (
	"strings"
)

// EnvField is one configurable variable of the repo.
type EnvField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`            // name contains KEY/SECRET/TOKEN/PASSWORD
	Default  string `json:"default,omitempty"` // non-empty default from the example file
	Optional bool   `json:"optional"`          // has a default — user may leave it blank
	System   bool   `json:"system"`            // PORT / *_PORT — platform-managed, not shown
}

// envExampleCandidates are tried in order via the contents API.
var envExampleCandidates = []string{".env.example", ".env.sample", ".env.template"}

// secretMarkers mark a variable as confidential (case-insensitive
// substring match on the variable name).
var secretMarkers = []string{"KEY", "SECRET", "TOKEN", "PASSWORD"}

// ParseEnvSchema parses .env.example-style content into form fields.
func ParseEnvSchema(content string) []EnvField {
	var out []EnvField
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, rest, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		value, comment := splitValueComment(strings.TrimSpace(rest))
		upper := strings.ToUpper(key)

		field := EnvField{
			Name:   key,
			Label:  comment,
			System: upper == "PORT" || strings.HasSuffix(upper, "_PORT"),
		}
		if field.Label == "" {
			field.Label = key
		}
		for _, m := range secretMarkers {
			if strings.Contains(upper, m) {
				field.Secret = true
				break
			}
		}
		if value != "" {
			field.Default = value
			field.Optional = true
		}
		out = append(out, field)
	}
	return out
}

// splitValueComment separates the value from an inline ` # comment`
// tail. Quoted values keep everything up to the closing quote; a
// comment may still follow it.
func splitValueComment(s string) (value, comment string) {
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "#") {
		// `KEY= # comment` — empty value, whole tail is the comment.
		return "", strings.TrimSpace(s[1:])
	}
	if q := s[0]; q == '"' || q == '\'' {
		if i := strings.IndexByte(s[1:], q); i >= 0 {
			value = s[1 : 1+i]
			comment = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s[2+i:]), "#"))
			return value, comment
		}
		// Unterminated quote — take the rest literally, no comment.
		return strings.Trim(s, "\"'"), ""
	}
	if i := strings.Index(s, " #"); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+2:])
	}
	return strings.Trim(s, "\"'"), ""
}
