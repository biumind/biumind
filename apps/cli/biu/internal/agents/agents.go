// Package agents loads file-based sub-agent definitions from
// ~/.biumind/agents/ and <project>/.biumind/agents/.
//
// Each agent is a single Markdown file with YAML frontmatter:
//
//   ---
//   name: explore
//   description: Read-only repo exploration. Use when the user wants
//     a survey rather than edits.
//   tools: Read, Glob, Grep, Bash
//   permissionMode: plan
//   model: inherit
//   maxTurns: 25
//   ---
//   You are the Explore sub-agent. Your job is to read files, run
//   ripgrep, and summarise findings — never to write or modify
//   anything. Format your reply as a numbered list.
//
// File body (post-frontmatter) is the agent's system prompt. It
// substitutes for the parent's system prompt during the spawned run.
//
// Naming convention: agentType = `name` field (or filename when
// missing). Names are case-sensitive; kebab-case is the canonical
// form.

package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

// Definition is one parsed agent record.
type Definition struct {
	// Name (agentType) is what the AgentTool's `subagent_type` arg
	// maps to. Case-sensitive.
	Name string

	// Description / WhenToUse is shown to the parent agent so it can
	// decide whether to spawn this type. Required.
	Description string

	// Tools is the whitelist this agent is allowed to call. Empty =
	// inherit the parent's full catalog. Names match the engine
	// registry (Read, Edit, Bash, etc).
	Tools []string

	// DisallowedTools subtracts from the inherited catalog when
	// Tools is empty. Useful for "everything except Bash".
	DisallowedTools []string

	// Model overrides the parent's model. "inherit" or "" = use the
	// parent's. Otherwise a model id like "claude-haiku-4-5".
	Model string

	// PermissionMode overrides the parent's mode for this run.
	// "plan" is common for read-only explore agents.
	PermissionMode permissions.Mode

	// MaxTurns caps the number of tool loops. 0 = inherit.
	MaxTurns int

	// SystemPrompt is the body of the markdown file (post-frontmatter).
	// Replaces the parent's system prompt for the duration of the
	// sub-agent run.
	SystemPrompt string

	// Skills auto-attached to this agent's session. Sourced from the
	// frontmatter `skills:` list (comma-separated). For now we
	// preserve the list — wiring into skills.Registry is a follow-up.
	Skills []string

	// Source records which layer the agent came from
	// (user / project). Higher-priority layers override lower.
	Source string

	// Path is the absolute filesystem path the agent was loaded from.
	Path string
}

// Registry is the set of all loaded agent definitions, keyed by Name.
// Project-layer entries override user-layer entries on conflict.
type Registry struct {
	byName map[string]*Definition
}

// NewRegistry returns an empty registry. Useful for tests.
func NewRegistry() *Registry { return &Registry{byName: map[string]*Definition{}} }

// Lookup returns the definition for agentType. ok=false when not
// registered. Callers should fall back to a default 'general-purpose'
// behaviour when not found.
func (r *Registry) Lookup(name string) (*Definition, bool) {
	if r == nil {
		return nil, false
	}
	d, ok := r.byName[name]
	return d, ok
}

// All returns every loaded definition sorted by name. Used by
// `/agents` slash + the engine's tool-catalog announcement.
func (r *Registry) All() []*Definition {
	if r == nil {
		return nil
	}
	out := make([]*Definition, 0, len(r.byName))
	for _, d := range r.byName {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns just the agentType strings, sorted. Cheap helper for
// AgentTool's input-schema enumeration.
func (r *Registry) Names() []string {
	all := r.All()
	out := make([]string, 0, len(all))
	for _, d := range all {
		out = append(out, d.Name)
	}
	return out
}

// Load walks the user + project agent directories and populates a
// fresh registry. Missing directories are not errors. Files with
// invalid frontmatter are skipped with a stderr warning so a single
// broken agent doesn't break the rest.
//
// `cwd` is the project root. Empty cwd skips the project layer.
//
// Resolution order: built-ins → user → project. Each layer overrides
// the previous on Name conflict, so a project file with name=Plan
// supersedes the baked-in definition.
func Load(cwd string) (*Registry, error) {
	r := NewRegistry()

	// Built-ins seed first so user / project files can override them
	// by reusing the same Name.
	r.seedBuiltins()

	// User layer next — project entries overwrite by name.
	if home, err := os.UserHomeDir(); err == nil {
		if err := r.loadDir(filepath.Join(home, ".biumind", "agents"), "user"); err != nil {
			return nil, err
		}
	}
	if cwd != "" {
		if err := r.loadDir(filepath.Join(cwd, ".biumind", "agents"), "project"); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// LoadDir is the public entry point for layer-by-layer loading.
// Used by the plugins aggregator (PP3) to attach a plugin's agents/
// directory after Load() has already populated user + project. The
// source string is stored verbatim on each Definition.Source so
// downstream tooling (`/plugin show`, telemetry) can see provenance;
// pass "plugin:<name>" to keep cross-plugin entries distinguishable.
//
// Missing dir is not an error (returns nil) — keeps the caller code
// in the aggregator small (no errors.Is(...) noise around a path
// the user simply didn't populate).
func (r *Registry) LoadDir(dir, source string) error {
	return r.loadDir(dir, source)
}

// loadDir scans dir/*.md (non-recursive, flat layout) and adds
// each parseable file. Returns an error only when the directory
// itself can't be read.
func (r *Registry) loadDir(dir, source string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("agents: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		def, err := loadFile(path, source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[biu] agent %s: %v\n", path, err)
			continue
		}
		if def == nil {
			// Frontmatter missing required fields — silently skip
			// ambiguously-shaped files.
			continue
		}
		r.byName[def.Name] = def
	}
	return nil
}

// loadFile parses one agent file. Returns nil + nil error when the
// file isn't shaped like an agent (no name in frontmatter).
func loadFile(path, source string) (*Definition, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	front, body := splitFrontmatter(string(raw))
	def := &Definition{
		Source: source, Path: path,
		SystemPrompt: strings.TrimSpace(body),
	}

	def.Name = front["name"]
	if def.Name == "" {
		// Fallback to filename without .md.
		base := filepath.Base(path)
		def.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	def.Description = front["description"]
	if def.Description == "" {
		// Description is required; enforce the same rule here.
		return nil, nil
	}

	def.Tools = parseList(front["tools"])
	def.DisallowedTools = parseList(front["disallowedTools"])
	def.Skills = parseList(front["skills"])
	def.Model = strings.TrimSpace(front["model"])
	if mode := strings.TrimSpace(front["permissionMode"]); mode != "" {
		def.PermissionMode = permissions.ModeFromString(mode)
	}
	if v := front["maxTurns"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			def.MaxTurns = n
		}
	}
	return def, nil
}

// parseList tolerates both YAML-list (`[a, b, c]`) and comma-
// separated (`a, b, c`) forms in frontmatter. Empty input → empty
// slice. Returns trimmed names with empties dropped.
func parseList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Strip optional bracket form.
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

// splitFrontmatter parses the leading `---\n...\n---\n` YAML-ish
// block into a flat map. Identical implementation to skills + output
// — duplicated to keep packages self-contained (no cycle).
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
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
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

// Apply layers an agent's overrides onto a base spawn request.
// Returns the modified request — caller passes it to AgentSpawner.
// `base` is the prompt-default request (parent's system / model /
// budget); the agent definition's non-zero fields override.
func (d *Definition) Apply(base SpawnRequest) SpawnRequest {
	out := base
	if d == nil {
		return out
	}
	if d.SystemPrompt != "" {
		out.System = d.SystemPrompt
	}
	if d.Model != "" && d.Model != "inherit" {
		out.Model = d.Model
	}
	if d.MaxTurns > 0 {
		out.MaxTurns = d.MaxTurns
	}
	if d.PermissionMode != "" {
		out.PermissionMode = d.PermissionMode
	}
	out.Tools = d.Tools
	out.DisallowedTools = d.DisallowedTools
	return out
}

// SpawnRequest is the minimal information the AgentTool / engine
// spawner needs to honour an agent type override. Defined here (not
// in engine) so this package stays free of engine imports — engine
// consumes a fully-resolved value built via Apply.
type SpawnRequest struct {
	System          string
	Model           string
	MaxTurns        int
	PermissionMode  permissions.Mode
	Tools           []string // whitelist
	DisallowedTools []string
}

// FilterTools returns the catalog reduced by the agent's allow / deny
// lists. Empty tools = no whitelist (allow everything in `available`
// minus DisallowedTools).
func (d *Definition) FilterTools(available []string) []string {
	if d == nil {
		return available
	}
	deny := map[string]bool{}
	for _, t := range d.DisallowedTools {
		deny[t] = true
	}
	if len(d.Tools) == 0 {
		out := make([]string, 0, len(available))
		for _, t := range available {
			if !deny[t] {
				out = append(out, t)
			}
		}
		return out
	}
	allow := map[string]bool{}
	for _, t := range d.Tools {
		allow[t] = true
	}
	out := make([]string, 0, len(d.Tools))
	for _, t := range available {
		if allow[t] && !deny[t] {
			out = append(out, t)
		}
	}
	return out
}

// Context is a tiny interface for tools that need the registry but
// can't import this package directly (avoid cycles). The engine
// satisfies it via a shim.
type Context interface {
	Lookup(agentType string) (*Definition, bool)
	Names() []string
}

var _ Context = (*Registry)(nil)
var _ context.Context = (context.Context)(nil) // unused but keeps imports honest if removed
