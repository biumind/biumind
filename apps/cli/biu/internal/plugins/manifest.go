// Plugin manifest schema + parser + validator.
//
// On-disk layout:
//
//	<plugin-dir>/
//	├── .claude-plugin/plugin.json   ← preferred location (ecosystem-compat)
//	├── plugin.json                  ← fallback location
//	├── commands/<name>.md           ← optional; same format as biu user commands
//	├── agents/<name>.md             ← optional; same format as biu user agents
//	├── skills/<name>/SKILL.md       ← optional
//	├── hooks/                       ← optional; hook handler scripts
//	├── output-styles/<name>.md      ← optional
//	└── README.md                    ← informational
//
// Manifest fields are intentionally a *subset* of the upstream
// schema. Fields biu doesn't yet consume (channels, lspServers,
// userConfig) parse via json.RawMessage so a third-party plugin
// doesn't fail validation on biu — it just loses those features
// until biu grows the matching subsystem. See
// PluginManifest.Unrecognised for the carrier.
package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PluginManifest is the parsed plugin.json. Field tags match the
// upstream schema verbatim so a wire-level drop-in works.
type PluginManifest struct {
	// ─── identity ─────────────────────────────────────────────

	// Name is the plugin's stable identifier. Lowercase letters,
	// digits, and hyphens; 1–64 chars; no leading/trailing hyphen.
	// This is what users type in `/plugin enable <name>` and what
	// the aggregator uses to detect collisions across sources.
	Name string `json:"name"`

	// Version is a semver-ish string. We don't enforce strict semver
	// (existing plugins are loose: "1.0.0", "0.1", "v0.1.0") but we do
	// require non-empty so `/plugin list` never shows blank versions.
	Version string `json:"version"`

	// Description is shown in `/plugin list` and `/plugin show`.
	// One line, ≤256 chars enforced — multi-paragraph descriptions
	// belong in README.md.
	Description string `json:"description,omitempty"`

	Author     Author `json:"author"`
	Homepage   string `json:"homepage,omitempty"`
	Repository string `json:"repository,omitempty"`
	License    string `json:"license,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`

	// ─── component path overrides ────────────────────────────
	//
	// Empty means "use convention" (commands/, agents/, …). Non-empty
	// must be a relative path inside the plugin directory; absolute
	// paths and "../" escapes are refused at load time.

	CommandsPath     string `json:"commandsPath,omitempty"`
	AgentsPath       string `json:"agentsPath,omitempty"`
	SkillsPath       string `json:"skillsPath,omitempty"`
	OutputStylesPath string `json:"outputStylesPath,omitempty"`

	// ─── inline components ───────────────────────────────────

	// Hooks is the same JSON shape settings.json's `hooks` field
	// has. Stored as RawMessage so this package doesn't re-implement
	// the hook parser — hooks.Registry.MergeJSON handles it in PP3.
	Hooks json.RawMessage `json:"hooks,omitempty"`

	// McpServers maps server-name → spec. Uses the leaf McpServerSpec
	// so plugins package stays free of internal/config imports.
	McpServers map[string]McpServerSpec `json:"mcpServers,omitempty"`

	// Settings carries plugin-author-defined defaults the user can
	// override in their settings.json under
	// `plugins.configs.<name>`. Currently opaque to biu — exposed in
	// `/plugin show` and forwarded to hook handlers via env. Future
	// PRs can grow a userConfig schema.
	Settings map[string]any `json:"settings,omitempty"`

	// ─── carrier for foreign manifest fields ─────────────────
	//
	// json.Decoder doesn't preserve unknown fields by default; we
	// catch them in a second pass (see UnmarshalJSON) so plugins
	// that declare channels / lspServers / userConfig parse cleanly.
	// Aggregator ignores Unrecognised; `/plugin show` displays the
	// keys for transparency.
	Unrecognised map[string]json.RawMessage `json:"-"`
}

// nameRE matches the lowercase-hyphen identifier used everywhere a
// plugin name appears in URLs, settings keys, and CLI args. Conservative
// on purpose — easier to relax later than to retract.
var nameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// known is the set of JSON keys PluginManifest claims. Anything not
// in this set lands in Unrecognised. Keep in sync with the struct
// tags above.
var knownManifestKeys = map[string]struct{}{
	"name": {}, "version": {}, "description": {}, "author": {},
	"homepage": {}, "repository": {}, "license": {}, "keywords": {},
	"commandsPath": {}, "agentsPath": {}, "skillsPath": {}, "outputStylesPath": {},
	"hooks": {}, "mcpServers": {}, "settings": {},
}

// UnmarshalJSON parses into the typed struct AND keeps every other
// top-level key in Unrecognised. This makes plugins with `channels`
// / `lspServers` / `userConfig` blocks round-trip without validation
// noise — biu just doesn't act on those keys (yet).
func (m *PluginManifest) UnmarshalJSON(data []byte) error {
	// Two-pass: first decode into an alias type to populate known
	// fields without recursion, then decode into a map to catch
	// unknown keys.
	type alias PluginManifest
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*m = PluginManifest(a)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// First pass would have caught this; defensive only.
		return err
	}
	for k, v := range raw {
		if _, ok := knownManifestKeys[k]; ok {
			continue
		}
		if m.Unrecognised == nil {
			m.Unrecognised = map[string]json.RawMessage{}
		}
		m.Unrecognised[k] = v
	}
	return nil
}

// Validate returns nil when the manifest is acceptable. Multiple
// problems are batched into one *ValidationError so users see the
// full list at once instead of fix-one-rerun cycles.
//
// Path-escape checks (commandsPath = "../sneak") are done here,
// not in loader.go, so the same validation runs whether the
// manifest came from disk, an HTTP fetch, or an in-memory test.
func (m *PluginManifest) Validate() error {
	v := &ValidationError{}

	if m.Name == "" {
		v.add("name", "required")
	} else if !nameRE.MatchString(m.Name) {
		v.add("name", "must match ^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$")
	}

	if m.Version == "" {
		v.add("version", "required")
	}

	if m.Description != "" && len(m.Description) > 256 {
		v.add("description", fmt.Sprintf("≤256 chars (got %d); use README.md for long-form", len(m.Description)))
	}

	if m.Author.Name == "" {
		v.add("author.name", "required")
	}

	for _, p := range []struct {
		field, val string
	}{
		{"commandsPath", m.CommandsPath},
		{"agentsPath", m.AgentsPath},
		{"skillsPath", m.SkillsPath},
		{"outputStylesPath", m.OutputStylesPath},
	} {
		if p.val == "" {
			continue
		}
		if filepath.IsAbs(p.val) {
			v.add(p.field, "must be relative to plugin root")
			continue
		}
		// Reject "..", "/..", and "..\" anywhere in the path. We
		// canonicalise via filepath.Clean and then look for a
		// leading "../" segment — that's the only way an escape
		// can survive Clean().
		clean := filepath.Clean(p.val)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			v.add(p.field, "may not escape plugin root with '..'")
		}
	}

	for serverName := range m.McpServers {
		if !nameRE.MatchString(serverName) {
			v.add("mcpServers."+serverName, "name must match ^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$")
		}
	}

	if len(v.Fields) > 0 {
		return v
	}
	return nil
}

func (v *ValidationError) add(field, msg string) {
	v.Fields = append(v.Fields, FieldError{Field: field, Message: msg})
}

// ParseManifest reads + parses a manifest from a directory. Tries
// the conventional location first (.claude-plugin/plugin.json), falls
// back to a top-level plugin.json. Returns ErrManifestMissing when
// neither exists; that's distinguishable from a parse / validation
// error so callers can give a useful message ("not a plugin" vs
// "broken plugin").
//
// Validation runs before return — callers don't need a separate
// Validate() call. To bypass validation in tests, use ParseManifestBytes.
func ParseManifest(dir string) (*PluginManifest, error) {
	candidates := []string{
		filepath.Join(dir, ".claude-plugin", "plugin.json"),
		filepath.Join(dir, "plugin.json"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		return ParseManifestBytes(data)
	}
	return nil, ErrManifestMissing
}

// ParseManifestBytes is the in-memory variant of ParseManifest.
// Validates after decode and returns a *PluginManifest only on
// success. The error chain wraps ErrManifestInvalid for
// errors.Is checks.
func ParseManifestBytes(data []byte) (*PluginManifest, error) {
	var m PluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse plugin.json: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
