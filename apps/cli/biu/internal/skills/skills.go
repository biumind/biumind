// Package skills loads file-based skills from ~/.biumind/skills/ and
// <project>/.biumind/skills/.
//
// Layout:
//
//   ~/.biumind/skills/<name>/SKILL.md       — global, user-installed
//   <cwd>/.biumind/skills/<name>/SKILL.md    — checked-in to the repo
//
// SKILL.md format:
//
//   ---
//   name: hello
//   description: Greet the user.
//   when-to-use: Whenever the user needs a friendly hello.
//   user-invocable: true
//   ---
//   # Body
//
//   Replies with a friendly greeting that respects the locale.
//   Arguments: $ARGS
//
// `$ARGS` (and the alias `$1`) is substituted with the user-supplied
// argument string at invocation time. Unknown placeholders are left
// untouched.
//
// We deliberately don't support hook-bearing or paths-bearing skills
// in this first cut — those land alongside Phase D's auto-attach
// pipeline.

package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Skill is one loaded skill record.
type Skill struct {
	Name          string
	Description   string
	WhenToUse     string
	UserInvocable bool
	Source        string // user | project
	Path          string // absolute path to SKILL.md
	Body          string // post-frontmatter markdown body

	// Paths is the set of glob patterns from the `paths:` frontmatter
	// field. When any pattern matches the agent's cwd (or files
	// relative to it), the skill is auto-attached: its body is
	// concatenated onto the system prompt without the user having to
	// invoke `/<skill>` explicitly. Empty / all-`**` = "always
	// available, never auto-attach".
	//
	// Format: comma-separated globs (parsed by parseList). Trailing
	// `/**` is stripped — the `ignore`-style libraries treat `foo`
	// and `foo/**` identically, but `**` alone means match-all and
	// is dropped (so it doesn't auto-attach in every project).
	Paths []string
}

// Run substitutes args into the body and returns the expanded prompt.
func (s Skill) Run(_ context.Context, args string) (string, error) {
	if s.Body == "" {
		return "", errors.New("skill body is empty")
	}
	out := strings.ReplaceAll(s.Body, "$ARGS", args)
	out = strings.ReplaceAll(out, "$1", args)
	return out, nil
}

// Registry holds every successfully-loaded skill, keyed by name.
// The first occurrence wins on duplicate names — project skills are
// appended after user skills, so user-level always loses to project.
type Registry struct {
	byName map[string]Skill
}

// NewRegistry returns an empty registry — useful for tests.
func NewRegistry() *Registry { return &Registry{byName: map[string]Skill{}} }

// Lookup returns a skill by name. The returned RuntimeSkill satisfies
// interactive.Skill (and any other consumer that wants Name()/Run()).
func (r *Registry) Lookup(name string) (RuntimeSkill, bool) {
	s, ok := r.byName[name]
	return RuntimeSkill{Skill: s}, ok
}

// AutoAttach returns the body text of every skill whose `paths:`
// frontmatter matches `cwd` (or any of `recentlyTouched`). The
// returned blocks are concatenated by the engine into the system
// prompt at startup. Order is name-sorted for cache stability.
//
// Matching rules:
//
//   * Pattern with no glob characters matches when cwd has it as a
//     suffix or as a containing directory (so `apps/cli/biu` matches
//     for any path inside that subtree).
//   * Pattern with `*` / `?` / `**` runs filepath.Match against cwd
//     itself plus every entry in recentlyTouched.
//   * Skills with no Paths constraint never auto-attach — they
//     remain available via `/<skill>` invocation.
func (r *Registry) AutoAttach(cwd string, recentlyTouched ...string) []Skill {
	if r == nil {
		return nil
	}
	candidates := append([]string{cwd}, recentlyTouched...)
	out := make([]Skill, 0)
	for _, name := range r.sortedNames() {
		s := r.byName[name]
		if len(s.Paths) == 0 {
			continue
		}
		if matchesAny(s.Paths, candidates) {
			out = append(out, s)
		}
	}
	return out
}

// AutoAttachPrompt assembles the auto-attached skills into a single
// system-prompt block, ready to inject. Empty when nothing matched.
func (r *Registry) AutoAttachPrompt(cwd string, recentlyTouched ...string) string {
	skills := r.AutoAttach(cwd, recentlyTouched...)
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Auto-attached skills\n\n")
	b.WriteString("These skill instructions are pre-loaded for the current project.\n")
	for _, s := range skills {
		b.WriteString("\n## ")
		b.WriteString(s.Name)
		if s.Description != "" {
			b.WriteString(" — ")
			b.WriteString(s.Description)
		}
		b.WriteByte('\n')
		b.WriteString(strings.TrimSpace(s.Body))
		b.WriteByte('\n')
	}
	return b.String()
}

// sortedNames is a tiny helper used by AutoAttach so the iteration
// order is deterministic — important for prompt cache hits.
func (r *Registry) sortedNames() []string {
	names := make([]string, 0, len(r.byName))
	for k := range r.byName {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// matchesAny reports whether any of the supplied paths satisfies any
// of the glob patterns. Each pattern can be:
//
//   "literal/dir"     — substring match against each path
//   "*.go"            — filepath.Match against each path's basename
//   "src/**/*.go"     — `**`-aware regex against each path
func matchesAny(patterns, paths []string) bool {
	for _, pat := range patterns {
		for _, p := range paths {
			if matchOnePattern(pat, p) {
				return true
			}
		}
	}
	return false
}

func matchOnePattern(pat, target string) bool {
	if pat == "" || target == "" {
		return false
	}
	if !strings.ContainsAny(pat, "*?[") {
		// Plain prefix / substring match — works for `apps/cli/biu`
		// against `/Users/x/repo/apps/cli/biu/cmd/...`.
		return strings.Contains(target, pat)
	}
	if strings.Contains(pat, "**") {
		// Translate to a regex.
		regexPat := regexp.QuoteMeta(pat)
		regexPat = strings.ReplaceAll(regexPat, `\*\*`, `.*`)
		regexPat = strings.ReplaceAll(regexPat, `\*`, `[^/]*`)
		regexPat = strings.ReplaceAll(regexPat, `\?`, `.`)
		re, err := regexp.Compile("(?:^|/)" + regexPat + "(?:$|/)")
		if err != nil {
			return false
		}
		return re.MatchString(target)
	}
	// Fall back to filepath.Match against the basename + the full path.
	if ok, _ := filepath.Match(pat, target); ok {
		return true
	}
	if ok, _ := filepath.Match(pat, filepath.Base(target)); ok {
		return true
	}
	return false
}

// All returns a stable, name-sorted snapshot for slash-completion UIs.
func (r *Registry) All() []Skill {
	out := make([]Skill, 0, len(r.byName))
	for _, s := range r.byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RuntimeSkill wraps a Skill so it satisfies the Name()/Run() method
// set expected by consumers (interactive.Skill). The underlying
// struct already has a Name *field*, so we can't add a same-named
// method to it directly — hence the wrapper.
type RuntimeSkill struct{ Skill }

// Name returns the skill name (overrides the embedded field for
// methods purposes — the field stays accessible via .Skill.Name).
func (s RuntimeSkill) Name() string {
	return s.Skill.Name
}


// Load walks all three layers and returns a populated registry.
// Order is bundled → user → project, with later layers overwriting
// earlier on name collision. So a project-checked-in skill shadows
// a user one, which shadows a bundled one. Missing directories are
// not errors; bad SKILL.md files emit a warning to stderr but don't
// abort the load.
func Load(cwd string) (*Registry, error) {
	return LoadWithExtraDirs(cwd, nil)
}

// LoadWithExtraDirs is the cross-directory variant. extraDirs come
// from the live permission ctx (paths added via /add-dir, --add-dir,
// or settings.permissions.additionalDirectories). Each contributes
// its `.biumind/skills/` tree as an additional "extra" source so
// adding a sibling repo with /add-dir surfaces its skills in the
// catalog.
//
// Conflicts: same name in cwd wins over extra dirs (cwd is the
// "primary" working dir).
func LoadWithExtraDirs(cwd string, extraDirs []string) (*Registry, error) {
	r := NewRegistry()

	// Bundled layer first — ships inside the binary, always present.
	r.loadBundled()

	// User layer next.
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, ".biumind", "skills")
		if err := r.loadDir(dir, "user"); err != nil {
			return nil, err
		}
	}
	// Extra working dirs — tagged "extra" so they're visible in the
	// catalog but lose to project-layer entries with the same name.
	for _, d := range extraDirs {
		if d == "" || d == cwd {
			continue
		}
		dir := filepath.Join(d, ".biumind", "skills")
		if err := r.loadDir(dir, "extra"); err != nil {
			return nil, err
		}
	}
	// Project layer last — wins on conflict.
	if cwd != "" {
		dir := filepath.Join(cwd, ".biumind", "skills")
		if err := r.loadDir(dir, "project"); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// LoadDir is the public layered-load entry point. The plugins
// aggregator (PP3) calls this for each plugin's skills/ directory
// after Load() has populated bundled + user + project. Source is
// stored on each Skill so /plugin show can attribute provenance;
// pass "plugin:<name>" for plugin-contributed skills.
//
// Missing dir is not an error.
func (r *Registry) LoadDir(dir, source string) error {
	return r.loadDir(dir, source)
}

// loadDir walks dir/<skill-name>/SKILL.md and registers each. Returns
// an error only on filesystem trouble (not parse errors).
func (r *Registry) loadDir(dir, source string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		s, err := loadFile(path, source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[biu] skill %s: %v\n", path, err)
			continue
		}
		if s.Name == "" {
			s.Name = e.Name() // fallback: use directory name
		}
		r.byName[s.Name] = s
	}
	return nil
}

// loadFile reads + parses one SKILL.md.
func loadFile(path, source string) (Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	return parseSkillBytes(raw, source, path), nil
}

// parsePathsFrontmatter normalises the comma-separated `paths:` field:
// strip trailing `/**`, drop empty entries and bare `**`/`*` (treated
// as match-all), and return nil when nothing scoped is left.
func parsePathsFrontmatter(s string) []string {
	patterns := parseList(s)
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/**")
		// Standalone wildcard means "everywhere" — drop it; the whole
		// list is treated as undefined when only wildcards remain.
		if p == "" || p == "**" || p == "*" {
			continue
		}
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// parseList tolerates `[a, b, c]`, `a, b, c`, and `"a","b"` forms.
// (Duplicates the helper from agents pkg — kept private here to
// avoid a cycle.)
func parseList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitFrontmatter parses the leading `---\n...---\n` YAML-ish block
// into a flat map. We keep it minimal — only top-level
// `key: value` lines are recognised. Anything more elaborate
// (lists, nested objects) is ignored — only flat top-level keys are
// needed for SKILL.md.
func splitFrontmatter(src string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(src, "---") {
		return out, src
	}
	// Find the closing delimiter.
	rest := src[3:]
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return out, src
	}
	frontText := strings.TrimSpace(rest[:idx])
	body := rest[idx+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")

	for _, line := range strings.Split(frontText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		val = strings.Trim(val, `"'`)
		out[key] = val
	}
	return out, body
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true
	}
	return false
}
