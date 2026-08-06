// Single-plugin loader.
//
// Load(dir) reads one plugin directory and returns a LoadedPlugin:
// manifest parsed, component paths resolved (absolute, verified to
// exist), hooks JSON normalised + ${PLUGIN_ROOT} expanded, MCP
// server commands expanded.
//
// Two manifest discovery modes:
//
//  1. plugin.json present (preferred):
//     Parse manifest. Use its commandsPath / agentsPath / etc.
//     when set; otherwise fall back to convention directories
//     (commands/, agents/, …).
//
//  2. No plugin.json (manifestless):
//     Synthesise a minimal manifest from the directory basename.
//     This supports manifestless convention plugins: some ship no
//     plugin.json, just a skills/ directory. Refusing to load it
//     would force users to write a manifest for every drop-in
//     plugin, which defeats the convention-over-configuration
//     ergonomic.
//
// External hooks file: the convention stores hooks at
// <plugin>/hooks/hooks.json (the file inside the conventional hooks/
// dir, not inline in plugin.json). The loader merges that file into
// the LoadedPlugin's HooksJSON when the manifest didn't declare hooks
// inline. Inline always wins — explicit beats implicit.
package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// jsonUnmarshal / jsonMarshal are package-level indirections so
// expand.go's expandJSON can use them without importing encoding/json
// (keeps that file's surface tight). No behavioural difference from
// the stdlib.
var (
	jsonUnmarshal = json.Unmarshal
	jsonMarshal   = json.Marshal
)

// Load reads one plugin from dir and returns a fully-resolved
// LoadedPlugin. The Source field is left as zero — callers (the
// aggregator in PP3, or `biu plugin show`) set it based on which
// search root the plugin lived under.
//
// Error semantics:
//
//	ErrManifestMissing   — no manifest AND no convention directories
//	                       to synthesise from. Caller likely passed a
//	                       wrong dir.
//	ErrManifestInvalid   — manifest parsed but failed schema; wraps
//	                       *ValidationError.
//	ErrPathEscape        — a path escaped the plugin root; refused.
//	other                — filesystem error (permission, ENOENT on
//	                       a declared component path, etc.).
//
// dir must be an absolute path. We canonicalise via filepath.Abs
// for the error path display but keep the original for the
// LoadedPlugin.Path field — the aggregator wants the path the user
// supplied (so /plugin list shows the configured directory, not a
// resolved one).
func Load(dir string) (*LoadedPlugin, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", dir, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", abs, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	manifest, err := loadOrSynthesise(abs)
	if err != nil {
		return nil, err
	}

	lp := &LoadedPlugin{
		Manifest: *manifest,
		Path:     abs,
		Enabled:  true, // settings overlay (PP4) flips this when disabled
	}

	if err := resolveComponentPaths(lp); err != nil {
		return nil, err
	}

	if err := loadHooks(lp); err != nil {
		return nil, err
	}

	if err := expandMcpServers(lp); err != nil {
		return nil, err
	}

	return lp, nil
}

// loadOrSynthesise returns the manifest from disk, or synthesises
// one from the directory basename when neither plugin.json variant
// exists. A synthesised manifest carries Author{Name: "unknown"} and
// Version "0.0.0" so the validator passes; downstream surfaces
// ("biu plugin list") can detect it via Manifest.Description == "".
func loadOrSynthesise(dir string) (*PluginManifest, error) {
	m, err := ParseManifest(dir)
	if err == nil {
		return m, nil
	}
	if !errors.Is(err, ErrManifestMissing) {
		return nil, err
	}

	// Synthesis path: only allow when at least one convention dir
	// exists. A directory with no plugin.json AND no commands/
	// agents/ skills/ hooks/ output-styles/ subdirs is almost
	// certainly not a plugin and should keep the original
	// ErrManifestMissing error.
	if !hasConventionDir(dir) {
		return nil, ErrManifestMissing
	}

	name := filepath.Base(dir)
	if !nameRE.MatchString(name) {
		return nil, fmt.Errorf("%w: directory %q is not a valid plugin name and no plugin.json supplies one", ErrManifestInvalid, name)
	}
	return &PluginManifest{
		Name:    name,
		Version: "0.0.0",
		Author:  Author{Name: "unknown"},
	}, nil
}

// hasConventionDir checks for any of the conventional component
// subdirectories. Used by the synthesis path to distinguish a real
// (manifestless) plugin directory from a random folder.
func hasConventionDir(dir string) bool {
	for _, d := range []string{"commands", "agents", "skills", "hooks", "output-styles"} {
		if st, err := os.Stat(filepath.Join(dir, d)); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// resolveComponentPaths populates LoadedPlugin's per-component
// absolute paths. For each component:
//
//   - If manifest declares an override path: validate it doesn't
//     escape the plugin root, join to root, verify it exists.
//   - Otherwise: probe the convention directory; populate only if
//     it exists. Missing convention dirs are NOT errors — most
//     plugins ship just one or two components.
//
// Returns ErrPathEscape (wrapped) on a "../" escape attempt; caller
// can errors.Is-check.
func resolveComponentPaths(lp *LoadedPlugin) error {
	type componentSpec struct {
		field    *string
		manifest string
		conv     string
	}
	specs := []componentSpec{
		{&lp.CommandsPath, lp.Manifest.CommandsPath, "commands"},
		{&lp.AgentsPath, lp.Manifest.AgentsPath, "agents"},
		{&lp.SkillsPath, lp.Manifest.SkillsPath, "skills"},
		{&lp.OutputStylesPath, lp.Manifest.OutputStylesPath, "output-styles"},
	}
	for _, s := range specs {
		var rel string
		if s.manifest != "" {
			rel = s.manifest
		} else {
			rel = s.conv
		}
		abs, err := safeJoin(lp.Path, rel)
		if err != nil {
			return err
		}
		st, err := os.Stat(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if s.manifest != "" {
					// Manifest explicitly declared this component
					// but the dir is missing — that's a hard error
					// (the plugin author is asking for something
					// that doesn't exist).
					return fmt.Errorf("%w: declared component path %q does not exist", ErrManifestInvalid, s.manifest)
				}
				// Convention dir absent → component not present.
				continue
			}
			return fmt.Errorf("stat %s: %w", abs, err)
		}
		if !st.IsDir() {
			return fmt.Errorf("%w: component path %q is not a directory", ErrManifestInvalid, rel)
		}
		*s.field = abs
	}
	return nil
}

// loadHooks resolves the hooks JSON for a plugin. Precedence:
//
//  1. Inline `hooks` field in plugin.json (already in
//     lp.Manifest.Hooks as RawMessage). Wins outright.
//  2. External <plugin>/hooks/hooks.json (conventional location).
//     Loaded and the top-level "hooks" object is extracted — the
//     file wraps the same shape under a description + hooks pair.
//
// In both cases ${CLAUDE_PLUGIN_ROOT} / ${BIU_PLUGIN_ROOT} are
// expanded so PP3's hooks.Registry.MergeJSON receives ready-to-run
// commands.
func loadHooks(lp *LoadedPlugin) error {
	// Inline path.
	if len(lp.Manifest.Hooks) > 0 {
		expanded, err := expandJSON(lp.Manifest.Hooks, lp.Path)
		if err != nil {
			return fmt.Errorf("expand inline hooks: %w", err)
		}
		lp.HooksJSON = expanded
		return nil
	}

	// External path.
	external := filepath.Join(lp.Path, "hooks", "hooks.json")
	data, err := os.ReadFile(external)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no hooks at all; fine
		}
		return fmt.Errorf("read %s: %w", external, err)
	}

	// The external hooks.json wraps the per-event map under a
	// "hooks" key alongside a "description". Extract just the inner
	// map so callers see the same shape whether hooks came from
	// inline manifest or external file.
	var wrapper struct {
		Hooks json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return fmt.Errorf("parse %s: %w", external, err)
	}
	if len(wrapper.Hooks) == 0 {
		// Some plugins might write the bare hooks object at the
		// top level (no description wrapper). Treat the whole file
		// as the hooks payload in that case — but only if it looks
		// like an event map (has at least one known event key).
		if looksLikeHookEventMap(data) {
			wrapper.Hooks = data
		} else {
			return nil
		}
	}

	expanded, err := expandJSON(wrapper.Hooks, lp.Path)
	if err != nil {
		return fmt.Errorf("expand %s: %w", external, err)
	}
	lp.HooksJSON = expanded
	return nil
}

// looksLikeHookEventMap returns true when raw is a JSON object with
// at least one well-known hook event name as a key. Used to detect
// "bare event map" hooks.json files that lack the description+hooks
// wrapper.
func looksLikeHookEventMap(raw []byte) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	for _, ev := range []string{
		"PreToolUse", "PostToolUse", "PostToolUseFailure",
		"UserPromptSubmit", "Stop", "StopFailure",
		"SessionStart", "SessionEnd",
		"PreCompact", "PostCompact",
		"Notification",
		"SubagentStart", "SubagentStop",
		"PermissionRequest", "PermissionDenied",
		"TaskCreated", "TaskCompleted",
		"FileChanged", "CwdChanged", "TeammateIdle",
	} {
		if _, ok := m[ev]; ok {
			return true
		}
	}
	return false
}

// expandMcpServers walks lp.Manifest.McpServers and substitutes
// plugin-root tokens in command, args, env values, url, and headers.
// The result is stored in lp.McpServers (a fresh map; the manifest
// stays unmodified for round-trip integrity).
func expandMcpServers(lp *LoadedPlugin) error {
	if len(lp.Manifest.McpServers) == 0 {
		return nil
	}
	out := make(map[string]McpServerSpec, len(lp.Manifest.McpServers))
	for name, spec := range lp.Manifest.McpServers {
		out[name] = McpServerSpec{
			Transport: spec.Transport,
			Command:   expandVars(spec.Command, lp.Path),
			Args:      expandStringSlice(spec.Args, lp.Path),
			Env:       expandStringMap(spec.Env, lp.Path),
			Cwd:       expandVars(spec.Cwd, lp.Path),
			URL:       expandVars(spec.URL, lp.Path),
			Headers:   expandStringMap(spec.Headers, lp.Path),
		}
	}
	lp.McpServers = out
	return nil
}

// safeJoin returns filepath.Join(root, rel) but refuses to return a
// path outside root. Returns ErrPathEscape (wrapped) when rel
// contains "../" segments that take it above root, or when rel is
// absolute.
//
// Why custom: filepath.Join("/root", "../x") returns "/x" silently
// — exactly the escape we want to refuse.
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q is absolute", ErrPathEscape, rel)
	}
	full := filepath.Join(root, rel)
	// Re-clean both, then verify full is root or a descendant.
	cleanRoot := filepath.Clean(root)
	cleanFull := filepath.Clean(full)
	if cleanFull != cleanRoot && !hasPrefixPath(cleanFull, cleanRoot) {
		return "", fmt.Errorf("%w: %q resolves outside plugin root", ErrPathEscape, rel)
	}
	return full, nil
}

// hasPrefixPath returns true when child is a path-component prefix
// of parent. Avoids the strings.HasPrefix footgun where "/foo" looks
// like a prefix of "/foobar".
func hasPrefixPath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	// If rel starts with "..", child is above parent.
	if rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return false
	}
	return true
}
