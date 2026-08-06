// Package commands loads user-defined slash commands from
// ~/.biumind/commands/<name>.md (and the project-local override at
// <cwd>/.biumind/commands/<name>.md).
//
// Each file is a Markdown body with optional YAML frontmatter:
//
//   ---
//   description: Refactor a function while preserving behaviour
//   ---
//   Refactor the target named below. Maintain the public API; if
//   behaviour must change, call it out explicitly.
//
//   Target: $ARGUMENTS
//
// When the user types `/refactor pkg/auth/jwt.go`, the REPL looks
// up `refactor`, performs `$ARGUMENTS` / `$CWD` / `$DATE`
// substitution on the body, and sends the result to the engine as
// a normal user prompt.

package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source labels where a command came from. Project commands shadow
// user commands of the same name (matches the agents/memory layer
// precedence). Plugin sources use "plugin:<name>" so /plugin show
// can attribute each command to its plugin.
type Source string

const (
	SrcUser    Source = "user"
	SrcProject Source = "project"
	// SrcPlugin is the prefix for plugin-contributed commands; combine
	// with the plugin name as `Source("plugin:" + name)` so the
	// aggregator (PP3) can tag each command with its origin.
	SrcPlugin Source = "plugin"
)

// Command is one loaded slash command. Body is the post-frontmatter
// markdown — already trimmed; substitutions are applied at Render
// time, not at load time.
type Command struct {
	Name        string
	Description string
	Body        string
	Path        string
	Source      Source
}

// Registry is a flat map of name → Command. Construct via Load;
// use Lookup / All / Names from the REPL slash dispatcher.
type Registry struct {
	byName map[string]*Command
}

// Load walks ~/.biumind/commands/ then <cwd>/.biumind/commands/
// and returns a populated Registry. Project commands win when both
// layers define the same name.
//
// Missing directories are not errors — first-run users have no
// commands. Bad markdown is logged via the returned error slice
// (not yet exposed to callers; future work).
func Load(cwd string) (*Registry, error) {
	r := &Registry{byName: map[string]*Command{}}

	if home, err := os.UserHomeDir(); err == nil {
		if err := r.scanDir(filepath.Join(home, ".biumind", "commands"), SrcUser); err != nil {
			return nil, err
		}
	}

	if cwd != "" {
		if err := r.scanDir(filepath.Join(cwd, ".biumind", "commands"), SrcProject); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// LoadDir is the public layered-load entry point. Used by the
// plugins aggregator to attach a plugin's commands/ directory after
// Load() has populated user + project. Source is typed Source —
// pass `Source("plugin:" + name)` for plugin-contributed commands.
//
// Missing dir is not an error.
func (r *Registry) LoadDir(dir string, source Source) error {
	return r.scanDir(dir, source)
}

// scanDir reads every *.md file in dir at the top level and adds
// each to the registry. Subdirectories are ignored — keeps the
// command namespace flat (no `/foo/bar` weirdness in slash names).
//
// On collisions, project source overwrites user (newer-source-wins
// fits the loader's left-to-right call order).
func (r *Registry) scanDir(dir string, source Source) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("commands: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if !validCommandName(name) {
			continue
		}
		front, body := splitFrontmatter(string(raw))
		desc := front["description"]
		if desc == "" {
			desc = firstNonEmptyLine(body)
		}
		r.byName[name] = &Command{
			Name:        name,
			Description: desc,
			Body:        strings.TrimSpace(body),
			Path:        path,
			Source:      source,
		}
	}
	return nil
}

// Lookup returns the Command for `name` (the slash without the
// leading `/`). ok=false on miss.
func (r *Registry) Lookup(name string) (*Command, bool) {
	if r == nil {
		return nil, false
	}
	c, ok := r.byName[name]
	return c, ok
}

// All returns commands in deterministic name order. Used by the
// REPL slash menu so the output doesn't shuffle between sessions.
func (r *Registry) All() []*Command {
	if r == nil {
		return nil
	}
	out := make([]*Command, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Render expands placeholders in the command body. Supported tokens
// (case-sensitive, $-prefixed):
//
//   $ARGUMENTS — verbatim string the user typed after the slash
//                ("/refactor pkg/auth.go" → "pkg/auth.go")
//   $CWD       — process cwd at render time (absolute path)
//   $DATE      — today's date in YYYY-MM-DD (UTC)
//
// Unknown $TOKENS are left as-is so the model sees them in the
// final prompt — that's the user's typo to fix, not biu's job to
// silently drop.
func (c *Command) Render(args string) string {
	if c == nil {
		return ""
	}
	out := c.Body
	out = strings.ReplaceAll(out, "$ARGUMENTS", args)
	out = strings.ReplaceAll(out, "$CWD", currentWorkdir())
	out = strings.ReplaceAll(out, "$DATE", time.Now().UTC().Format("2006-01-02"))
	return out
}

// validCommandName mirrors the rules biu uses elsewhere for things
// the LLM / shell will see: ASCII letters / digits / `-` / `_`,
// lead with a letter, ≤32 chars. Keeps slash names typeable.
func validCommandName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		case (r == '-' || r == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}

// splitFrontmatter pulls a tiny key:value YAML header out of the
// markdown front and returns (frontmatter map, body). Supports the
// common `--- … ---` fenced form. We don't pull in a YAML library
// for this — biu's command frontmatter only carries a handful of
// scalar fields, and a hand parser is faster + matches the
// existing memory/agents loaders' style.
func splitFrontmatter(s string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return out, s
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(s, "---\n"), "---\r\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		// Try Windows-style boundary.
		end = strings.Index(rest, "\r\n---\r\n")
		if end < 0 {
			return out, s
		}
	}
	header := rest[:end]
	body := rest[end:]
	body = strings.TrimPrefix(body, "\n---\n")
	body = strings.TrimPrefix(body, "\r\n---\r\n")

	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		k := strings.TrimSpace(line[:colon])
		v := strings.TrimSpace(line[colon+1:])
		// Strip surrounding quotes for values like `description: "x"`.
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"') {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, body
}

// firstNonEmptyLine extracts the first non-blank line from `s` for
// use as a fallback description when frontmatter is absent. Strips
// any leading "# " markers so a `# Refactor` heading becomes
// "Refactor" in the slash menu.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip markdown headers + bold markers.
		line = strings.TrimLeft(line, "# ")
		if len(line) > 100 {
			line = line[:97] + "…"
		}
		return line
	}
	return ""
}

// currentWorkdir returns os.Getwd or "" on error. Lives here as a
// var so tests can patch it; production rarely fails.
var currentWorkdir = func() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
