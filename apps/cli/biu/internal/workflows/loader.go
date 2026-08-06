// Package workflows loads multi-step task definitions from
// ~/.biumind/workflows/<name>.md (and the project override at
// <cwd>/.biumind/workflows/<name>.md).
//
// A workflow is a heavier sibling of a user-defined slash command
// (internal/commands). Where a command renders a single prompt,
// a workflow can additionally:
//
//   * declare pre-flight requirements that abort early when
//     unmet (e.g. "must be in a git repo", "tree must be clean").
//   * be previewed via `/workflow show <name>` so the user sees
//     the plan before kicking it off.
//   * carry an `args:` schema for documentation (today informational
//     only — args are still substituted as a single $ARGUMENTS
//     string; future versions may bind named placeholders).
//
// biu defines its own frontmatter shape + check vocabulary.

package workflows

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source labels where a workflow came from. Project workflows shadow
// user workflows of the same name (matches commands / agents).
type Source string

const (
	SrcUser    Source = "user"
	SrcProject Source = "project"
)

// Workflow is one loaded definition. Body is the markdown body
// post-frontmatter; substitutions are applied at Render time.
type Workflow struct {
	Name        string
	Description string
	Args        []ArgSpec  // documentation only today
	Requires    []string   // pre-flight check names
	Body        string
	Path        string
	Source      Source
}

// ArgSpec is one frontmatter argument entry. Pure documentation —
// surfaced by `/workflow show <name>` so users know what the
// workflow expects, but runtime substitution is still verbatim
// `$ARGUMENTS`.
type ArgSpec struct {
	Name        string
	Description string
}

// Registry is a flat map of name → Workflow.
type Registry struct {
	byName map[string]*Workflow
}

// Load walks the user + project workflow directories. Empty home /
// missing dirs are not errors — first-run users have no workflows.
func Load(cwd string) (*Registry, error) {
	r := &Registry{byName: map[string]*Workflow{}}
	if home, err := os.UserHomeDir(); err == nil {
		if err := r.scanDir(filepath.Join(home, ".biumind", "workflows"), SrcUser); err != nil {
			return nil, err
		}
	}
	if cwd != "" {
		if err := r.scanDir(filepath.Join(cwd, ".biumind", "workflows"), SrcProject); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) scanDir(dir string, source Source) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("workflows: read %s: %w", dir, err)
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
		if !validWorkflowName(name) {
			continue
		}
		front, body := splitFrontmatter(string(raw))
		w := &Workflow{
			Name:        name,
			Description: front["description"],
			Body:        strings.TrimSpace(body),
			Path:        path,
			Source:      source,
		}
		if w.Description == "" {
			w.Description = firstNonEmptyLine(body)
		}
		if reqRaw := front["requires"]; reqRaw != "" {
			w.Requires = parseList(reqRaw)
		}
		// args are line-form: each "- name: x; description: y" or
		// just "- x" (name only). The frontmatter splitter is too
		// minimal to parse YAML lists, so we reduce the args field
		// to a comma-separated names list and leave description
		// blank when the user uses the simpler form.
		if argsRaw := front["args"]; argsRaw != "" {
			for _, n := range parseList(argsRaw) {
				w.Args = append(w.Args, ArgSpec{Name: n})
			}
		}
		r.byName[name] = w
	}
	return nil
}

// Lookup returns the workflow for `name`. ok=false on miss.
func (r *Registry) Lookup(name string) (*Workflow, bool) {
	if r == nil {
		return nil, false
	}
	w, ok := r.byName[name]
	return w, ok
}

// All returns workflows in deterministic name order.
func (r *Registry) All() []*Workflow {
	if r == nil {
		return nil
	}
	out := make([]*Workflow, 0, len(r.byName))
	for _, w := range r.byName {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Render expands placeholders in the workflow body. Same set as
// commands: $ARGUMENTS, $CWD, $DATE.
//
// Run pre-flight checks separately via Verify before calling
// Render — Render does NOT enforce checks, by design (the caller
// might want to preview the rendered body even when the workspace
// fails the checks).
func (w *Workflow) Render(args string) string {
	if w == nil {
		return ""
	}
	out := w.Body
	out = strings.ReplaceAll(out, "$ARGUMENTS", args)
	out = strings.ReplaceAll(out, "$CWD", currentWorkdir())
	out = strings.ReplaceAll(out, "$DATE", time.Now().UTC().Format("2006-01-02"))
	return out
}

// Verify runs every pre-flight check declared in `requires`.
// Returns the first failure verbatim. Empty Requires → nil error.
//
// Recognised checks:
//
//   git_repo      — current cwd is inside a git working tree
//   clean_tree    — `git status --porcelain` is empty
//   staged_changes — `git diff --cached --name-only` non-empty
//
// Unknown check names yield an error so a typo doesn't silently
// pass the gate.
func (w *Workflow) Verify(cwd string) error {
	if w == nil {
		return nil
	}
	for _, name := range w.Requires {
		if err := runWorkflowCheck(name, cwd); err != nil {
			return fmt.Errorf("requires %s: %w", name, err)
		}
	}
	return nil
}

// runWorkflowCheck dispatches one named check. Public-ish (no
// CamelCase but not lowercase-only) so future packages can add
// their own checks without touching this dispatcher.
func runWorkflowCheck(name, cwd string) error {
	switch name {
	case "git_repo":
		return checkGitRepo(cwd)
	case "clean_tree":
		return checkCleanTree(cwd)
	case "staged_changes":
		return checkStagedChanges(cwd)
	default:
		return fmt.Errorf("unknown check %q (valid: git_repo / clean_tree / staged_changes)", name)
	}
}

func checkGitRepo(cwd string) error {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = cwd
	if err := cmd.Run(); err != nil {
		return errors.New("not inside a git work tree")
	}
	return nil
}

func checkCleanTree(cwd string) error {
	if err := checkGitRepo(cwd); err != nil {
		return err
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return errors.New("working tree has uncommitted changes")
	}
	return nil
}

func checkStagedChanges(cwd string) error {
	if err := checkGitRepo(cwd); err != nil {
		return err
	}
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return errors.New("no staged changes")
	}
	return nil
}

// validWorkflowName mirrors the slash-command rules so workflow
// names typeable as `/workflow <name>`.
func validWorkflowName(s string) bool {
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

// splitFrontmatter pulls a tiny key:value YAML header out and
// returns (frontmatter, body). Mirrors internal/commands but
// duplicated here to keep the package's dependency footprint
// minimal.
func splitFrontmatter(s string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return out, s
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(s, "---\n"), "---\r\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
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
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, body
}

// firstNonEmptyLine extracts the first non-blank line for the
// fallback description. Strips markdown header marks.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "# ")
		if len(line) > 100 {
			line = line[:97] + "…"
		}
		return line
	}
	return ""
}

// parseList accepts comma- / whitespace- / bracket-separated lists.
// Same shape internal/agents uses for tools fields.
func parseList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = strings.TrimPrefix(strings.TrimSuffix(s, "]"), "[")
	}
	out := []string{}
	for _, raw := range strings.Split(s, ",") {
		v := strings.Trim(strings.TrimSpace(raw), `"`)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

var currentWorkdir = func() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
