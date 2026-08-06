// Package output loads + applies "output styles" — short prompts that
// shape how the assistant frames its replies (concise, explanatory,
// teaching, etc).
//
// A style file is
// just a markdown document with optional frontmatter:
//
//   ---
//   name: concise
//   description: One-line answers, no commentary.
//   ---
//   Reply in a single sentence. Skip greetings.
//
// Files live under:
//
//   ~/.biumind/output-styles/<name>.md
//   <cwd>/.biumind/output-styles/<name>.md
//
// The selected style's body is appended to the engine's system
// prompt at session start. Switching styles mid-session is the
// REPL's job (slash command, /output-style <name>); the engine
// itself just consumes the assembled system text.

package output

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Style is one loaded output style.
type Style struct {
	Name        string
	Description string
	Prompt      string // body of the style file (post-frontmatter)
	Source      string // user | project | builtin
	Path        string // empty for builtin
}

// Registry indexes styles by name.
type Registry struct {
	byName map[string]Style
}

// NewRegistry returns the registry pre-populated with biu's built-in
// styles. Callers can layer user / project files on top via Load.
func NewRegistry() *Registry {
	r := &Registry{byName: map[string]Style{}}
	for _, b := range builtin() {
		r.byName[b.Name] = b
	}
	return r
}

// builtin is the curated list shipped with biu.
func builtin() []Style {
	return []Style{
		{
			Name:        "default",
			Description: "Standard responses, balanced detail.",
			Prompt:      "",
			Source:      "builtin",
		},
		{
			Name:        "concise",
			Description: "One-or-two-sentence answers; no preamble.",
			Prompt: "Reply as concisely as possible. No preambles, no recap, no closing pleasantries. " +
				"When code is the answer, show the code with at most one line of context.",
			Source: "builtin",
		},
		{
			Name:        "explanatory",
			Description: "Step-by-step reasoning with rationale.",
			Prompt: "Explain your reasoning step by step. Surface trade-offs and " +
				"why-this-not-that. Use short paragraphs and bullet points.",
			Source: "builtin",
		},
		{
			Name:        "teaching",
			Description: "Treats the user as learning the topic from scratch.",
			Prompt: "Assume the user is new to this topic. Define jargon the first " +
				"time it appears. Use small examples and check understanding before " +
				"adding complexity.",
			Source: "builtin",
		},
	}
}

// Load merges file-based styles from user + project layers. Project
// overrides user; user overrides builtin. Missing directories are
// fine; bad files emit a stderr warning but don't abort.
func (r *Registry) Load(cwd string) error {
	if home, err := os.UserHomeDir(); err == nil {
		_ = r.loadDir(filepath.Join(home, ".biumind", "output-styles"), "user")
	}
	if cwd != "" {
		_ = r.loadDir(filepath.Join(cwd, ".biumind", "output-styles"), "project")
	}
	return nil
}

// LoadDir is the public layered-load entry point. The plugins
// aggregator calls this for each plugin's output-styles/ directory.
// Pass "plugin:<name>" for plugin-contributed styles so /plugin show
// can attribute them. Missing dir is not an error.
func (r *Registry) LoadDir(dir, source string) error {
	return r.loadDir(dir, source)
}

func (r *Registry) loadDir(dir, source string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[biu] output-style %s: %v\n", path, err)
			continue
		}
		front, body := splitFrontmatter(string(raw))
		style := Style{
			Source: source, Path: path,
			Prompt: strings.TrimSpace(body),
		}
		style.Name = front["name"]
		if style.Name == "" {
			style.Name = strings.TrimSuffix(e.Name(), ".md")
		}
		style.Description = front["description"]
		r.byName[style.Name] = style
	}
	return nil
}

// Get returns a style by name. Falls back to "default" when not
// found so callers always have something to apply.
func (r *Registry) Get(name string) Style {
	if s, ok := r.byName[name]; ok {
		return s
	}
	return r.byName["default"]
}

// All returns a stable name-sorted snapshot.
func (r *Registry) All() []Style {
	out := make([]Style, 0, len(r.byName))
	for _, s := range r.byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Apply concatenates the style's prompt onto an existing system text.
// Empty prompts (e.g. the default style) leave the input unchanged.
func (s Style) Apply(system string) string {
	if s.Prompt == "" {
		return system
	}
	if system == "" {
		return s.Prompt
	}
	return system + "\n\n" + s.Prompt
}

// splitFrontmatter is the same trivial parser the skills package
// uses. Duplicated to avoid an import dependency between two
// otherwise-unrelated packages.
func splitFrontmatter(src string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(src, "---") {
		return out, src
	}
	rest := src[3:]
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return out, src
	}
	frontText := strings.TrimSpace(rest[:idx])
	body := strings.TrimPrefix(rest[idx+len("\n---"):], "\n")
	for _, line := range strings.Split(frontText, "\n") {
		line = strings.TrimSpace(line)
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue
		}
		k := strings.TrimSpace(line[:colon])
		v := strings.Trim(strings.TrimSpace(line[colon+1:]), `"'`)
		out[k] = v
	}
	return out, body
}
