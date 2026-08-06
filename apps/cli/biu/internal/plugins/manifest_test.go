package plugins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestBytes_minimal(t *testing.T) {
	data := []byte(`{
		"name": "code-review",
		"version": "1.0.0",
		"description": "Multi-agent PR review",
		"author": { "name": "BiuMind" }
	}`)
	m, err := ParseManifestBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Name != "code-review" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q", m.Version)
	}
	if m.Author.Name != "BiuMind" {
		t.Errorf("Author.Name = %q", m.Author.Name)
	}
}

func TestParseManifestBytes_unknownFieldsPreserved(t *testing.T) {
	// Third-party plugins ship with channels / lspServers / userConfig
	// that biu doesn't yet consume. They must round-trip without
	// rejecting the whole manifest.
	data := []byte(`{
		"name": "x",
		"version": "1",
		"author": { "name": "a" },
		"channels": { "telegram": { "token": "x" } },
		"userConfig": { "theme": "dark" },
		"lspServers": { "rust": {} }
	}`)
	m, err := ParseManifestBytes(data)
	if err != nil {
		t.Fatalf("unknown fields should not break parse: %v", err)
	}
	for _, k := range []string{"channels", "userConfig", "lspServers"} {
		if _, ok := m.Unrecognised[k]; !ok {
			t.Errorf("Unrecognised missing %q", k)
		}
	}
}

func TestValidate_required(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantBad string
	}{
		{
			name:    "missing name",
			body:    `{"version":"1","author":{"name":"a"}}`,
			wantBad: "name",
		},
		{
			name:    "missing version",
			body:    `{"name":"x","author":{"name":"a"}}`,
			wantBad: "version",
		},
		{
			name:    "missing author.name",
			body:    `{"name":"x","version":"1","author":{}}`,
			wantBad: "author.name",
		},
		{
			name:    "name uppercase",
			body:    `{"name":"CodeReview","version":"1","author":{"name":"a"}}`,
			wantBad: "name",
		},
		{
			name:    "name with underscore",
			body:    `{"name":"code_review","version":"1","author":{"name":"a"}}`,
			wantBad: "name",
		},
		{
			name:    "name leading hyphen",
			body:    `{"name":"-x","version":"1","author":{"name":"a"}}`,
			wantBad: "name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifestBytes([]byte(tc.body))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %T: %v", err, err)
			}
			found := false
			for _, f := range ve.Fields {
				if f.Field == tc.wantBad {
					found = true
				}
			}
			if !found {
				t.Errorf("expected field %q in errors, got %+v", tc.wantBad, ve.Fields)
			}
			if !errors.Is(err, ErrManifestInvalid) {
				t.Error("err chain should match ErrManifestInvalid")
			}
		})
	}
}

func TestValidate_pathEscape(t *testing.T) {
	for _, val := range []string{"../evil", "../../evil", "/abs/path", "./../sneak"} {
		body := `{
			"name": "x", "version": "1", "author": {"name":"a"},
			"commandsPath": "` + val + `"
		}`
		_, err := ParseManifestBytes([]byte(body))
		if err == nil {
			t.Errorf("path %q: want error", val)
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("path %q: want ValidationError, got %v", val, err)
			continue
		}
		hasField := false
		for _, f := range ve.Fields {
			if f.Field == "commandsPath" {
				hasField = true
			}
		}
		if !hasField {
			t.Errorf("path %q: expected commandsPath in errors, got %+v", val, ve.Fields)
		}
	}
}

func TestValidate_pathRelativeOK(t *testing.T) {
	body := `{
		"name":"x","version":"1","author":{"name":"a"},
		"commandsPath":"my-commands",
		"agentsPath":"sub/agents",
		"skillsPath":"."
	}`
	if _, err := ParseManifestBytes([]byte(body)); err != nil {
		t.Errorf("relative paths should validate: %v", err)
	}
}

func TestValidate_descriptionLength(t *testing.T) {
	long := strings.Repeat("x", 257)
	body := `{"name":"x","version":"1","author":{"name":"a"},"description":"` + long + `"}`
	_, err := ParseManifestBytes([]byte(body))
	if err == nil {
		t.Fatal("want error for >256 char description")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
}

func TestValidate_mcpServerName(t *testing.T) {
	body := `{
		"name":"x","version":"1","author":{"name":"a"},
		"mcpServers": { "BadName": { "command": "x" } }
	}`
	_, err := ParseManifestBytes([]byte(body))
	if err == nil {
		t.Fatal("want error for invalid mcp server name")
	}
}

func TestValidate_multipleErrors(t *testing.T) {
	// Empty body → name + version + author.name missing.
	_, err := ParseManifestBytes([]byte(`{}`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	if len(ve.Fields) < 3 {
		t.Errorf("expected ≥3 errors, got %d: %+v", len(ve.Fields), ve.Fields)
	}
}

// ─── ParseManifest (disk) ────────────────────────────────────

func TestParseManifest_preferredLocation(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"x","version":"1","author":{"name":"a"}}`
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), body)
	m, err := ParseManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "x" {
		t.Errorf("Name = %q", m.Name)
	}
}

func TestParseManifest_fallbackLocation(t *testing.T) {
	dir := t.TempDir()
	body := `{"name":"x","version":"1","author":{"name":"a"}}`
	mustWrite(t, filepath.Join(dir, "plugin.json"), body)
	m, err := ParseManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "x" {
		t.Errorf("Name = %q", m.Name)
	}
}

func TestParseManifest_preferredOverridesFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "plugin.json"), `{"name":"fallback","version":"1","author":{"name":"a"}}`)
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"preferred","version":"1","author":{"name":"a"}}`)
	m, err := ParseManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "preferred" {
		t.Errorf("preferred location must win, got %q", m.Name)
	}
}

func TestParseManifest_missing(t *testing.T) {
	dir := t.TempDir()
	_, err := ParseManifest(dir)
	if !errors.Is(err, ErrManifestMissing) {
		t.Errorf("want ErrManifestMissing, got %v", err)
	}
}

// ─── McpServerSpec round-trip ────────────────────────────────

func TestMcpServerSpec_parses(t *testing.T) {
	body := `{
		"name":"x","version":"1","author":{"name":"a"},
		"mcpServers": {
			"github": {
				"transport": "stdio",
				"command": "/usr/local/bin/gh-mcp",
				"args": ["--token", "x"],
				"env": { "GH_TOKEN": "y" }
			},
			"linear": {
				"transport": "http",
				"url": "https://mcp.linear.app",
				"headers": { "Authorization": "Bearer z" }
			}
		}
	}`
	m, err := ParseManifestBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	gh, ok := m.McpServers["github"]
	if !ok {
		t.Fatal("github server missing")
	}
	if gh.Command != "/usr/local/bin/gh-mcp" {
		t.Errorf("github.command = %q", gh.Command)
	}
	if len(gh.Args) != 2 {
		t.Errorf("github.args = %v", gh.Args)
	}
	linear, ok := m.McpServers["linear"]
	if !ok {
		t.Fatal("linear server missing")
	}
	if linear.URL != "https://mcp.linear.app" {
		t.Errorf("linear.url = %q", linear.URL)
	}
}

// ─── helpers ────────────────────────────────────────────────

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Confirm hooks JSON survives round-trip as RawMessage so PP3 can
// hand it straight to hooks.Registry.MergeJSON without re-parsing.
func TestParseManifest_hooksRoundTrip(t *testing.T) {
	body := `{
		"name":"x","version":"1","author":{"name":"a"},
		"hooks": {
			"PreToolUse": [{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi"}]}]
		}
	}`
	m, err := ParseManifestBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Hooks) == 0 {
		t.Fatal("Hooks empty")
	}
	// Re-decode to verify it's valid JSON with the expected shape.
	var got map[string]any
	if err := json.Unmarshal(m.Hooks, &got); err != nil {
		t.Fatalf("hooks not valid JSON: %v", err)
	}
	if _, ok := got["PreToolUse"]; !ok {
		t.Errorf("PreToolUse missing in round-trip")
	}
}
