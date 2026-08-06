package plugins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Load: happy paths ───────────────────────────────────────

func TestLoad_minimal(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name":"x","version":"1","author":{"name":"a"}}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if lp.Manifest.Name != "x" {
		t.Errorf("Name = %q", lp.Manifest.Name)
	}
	if !lp.Enabled {
		t.Error("Enabled default should be true")
	}
	if !filepath.IsAbs(lp.Path) {
		t.Errorf("Path should be absolute, got %q", lp.Path)
	}
}

func TestLoad_conventionDirsDiscovered(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name":"x","version":"1","author":{"name":"a"}}`)
	// Create commands/ and agents/ but not skills/ or output-styles/.
	for _, sub := range []string{"commands", "agents"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if lp.CommandsPath == "" {
		t.Error("CommandsPath should be populated when commands/ exists")
	}
	if lp.AgentsPath == "" {
		t.Error("AgentsPath should be populated when agents/ exists")
	}
	if lp.SkillsPath != "" {
		t.Error("SkillsPath should be empty when skills/ doesn't exist")
	}
	if lp.OutputStylesPath != "" {
		t.Error("OutputStylesPath should be empty when output-styles/ doesn't exist")
	}
}

func TestLoad_pathOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "my-cmds"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name":"x","version":"1","author":{"name":"a"},"commandsPath":"my-cmds"}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "my-cmds")
	if lp.CommandsPath != want {
		t.Errorf("CommandsPath = %q, want %q", lp.CommandsPath, want)
	}
}

func TestLoad_pathOverrideMissing(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name":"x","version":"1","author":{"name":"a"},"commandsPath":"missing"}`)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("want error when declared path is missing")
	}
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("err should wrap ErrManifestInvalid, got %v", err)
	}
}

// ─── Load: synthesised manifest ──────────────────────────────

func TestLoad_synthesisedManifest(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin-dev") // valid plugin name shape
	if err := os.MkdirAll(filepath.Join(plugin, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	lp, err := Load(plugin)
	if err != nil {
		t.Fatalf("synthesis should succeed when convention dir exists: %v", err)
	}
	if lp.Manifest.Name != "plugin-dev" {
		t.Errorf("Name should come from dirname, got %q", lp.Manifest.Name)
	}
	if lp.Manifest.Author.Name != "unknown" {
		t.Errorf("Synthesised Author.Name should be 'unknown', got %q", lp.Manifest.Author.Name)
	}
	if lp.SkillsPath == "" {
		t.Error("SkillsPath should be set even on synthesised plugin")
	}
}

func TestLoad_noManifestNoConventionDir(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "not-a-plugin")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Load(plugin)
	if !errors.Is(err, ErrManifestMissing) {
		t.Errorf("want ErrManifestMissing, got %v", err)
	}
}

func TestLoad_noManifestBadDirname(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "Bad_Name") // uppercase + underscore — invalid
	if err := os.MkdirAll(filepath.Join(plugin, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Load(plugin)
	if err == nil {
		t.Fatal("want error for invalid dirname")
	}
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("err should wrap ErrManifestInvalid, got %v", err)
	}
}

// ─── Load: hooks ──────────────────────────────────────────────

func TestLoad_inlineHooks(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{
		"name":"x","version":"1","author":{"name":"a"},
		"hooks": {
			"PreToolUse": [{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi"}]}]
		}
	}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lp.HooksJSON) == 0 {
		t.Fatal("HooksJSON empty")
	}
	var got map[string]any
	if err := json.Unmarshal(lp.HooksJSON, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["PreToolUse"]; !ok {
		t.Error("PreToolUse missing")
	}
}

func TestLoad_externalHooksWithWrapper(t *testing.T) {
	// Standard layout: hooks/hooks.json wrapping description+hooks.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name":"x","version":"1","author":{"name":"a"}}`)
	mustWrite(t, filepath.Join(dir, "hooks", "hooks.json"), `{
		"description": "test",
		"hooks": {
			"Stop": [{"hooks":[{"type":"command","command":"python3 ${CLAUDE_PLUGIN_ROOT}/hooks/stop.py"}]}]
		}
	}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lp.HooksJSON) == 0 {
		t.Fatal("HooksJSON should be populated from external hooks.json")
	}
	if !strings.Contains(string(lp.HooksJSON), lp.Path+"/hooks/stop.py") {
		t.Errorf("${CLAUDE_PLUGIN_ROOT} not expanded; HooksJSON = %s", lp.HooksJSON)
	}
}

func TestLoad_externalHooksBareEventMap(t *testing.T) {
	// Some plugins might write the bare event map without wrapper.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name":"x","version":"1","author":{"name":"a"}}`)
	mustWrite(t, filepath.Join(dir, "hooks", "hooks.json"), `{
		"PreToolUse": [{"hooks":[{"type":"command","command":"echo"}]}]
	}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lp.HooksJSON) == 0 {
		t.Fatal("HooksJSON should be populated from bare event map")
	}
}

func TestLoad_inlineHooksWinOverExternal(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{
		"name":"x","version":"1","author":{"name":"a"},
		"hooks": { "PreToolUse": [{"hooks":[{"type":"command","command":"INLINE"}]}] }
	}`)
	mustWrite(t, filepath.Join(dir, "hooks", "hooks.json"), `{
		"hooks": { "PreToolUse": [{"hooks":[{"type":"command","command":"EXTERNAL"}]}] }
	}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lp.HooksJSON), "INLINE") {
		t.Errorf("inline hooks should win, got %s", lp.HooksJSON)
	}
	if strings.Contains(string(lp.HooksJSON), "EXTERNAL") {
		t.Errorf("external hooks should be ignored when inline present")
	}
}

func TestLoad_biuPluginRootAlias(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{
		"name":"x","version":"1","author":{"name":"a"},
		"hooks": { "Stop": [{"hooks":[{"type":"command","command":"${BIU_PLUGIN_ROOT}/x.sh"}]}] }
	}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lp.HooksJSON), lp.Path+"/x.sh") {
		t.Errorf("${BIU_PLUGIN_ROOT} not expanded; got %s", lp.HooksJSON)
	}
}

// ─── Load: mcp servers ────────────────────────────────────────

func TestLoad_mcpServerExpansion(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{
		"name":"x","version":"1","author":{"name":"a"},
		"mcpServers": {
			"local": {
				"command": "${CLAUDE_PLUGIN_ROOT}/bin/server",
				"args": ["--root", "${BIU_PLUGIN_ROOT}/data"],
				"env": { "BASE": "${CLAUDE_PLUGIN_ROOT}" }
			}
		}
	}`)

	lp, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := lp.McpServers["local"]
	if got.Command != lp.Path+"/bin/server" {
		t.Errorf("Command = %q, want %q", got.Command, lp.Path+"/bin/server")
	}
	if got.Args[1] != lp.Path+"/data" {
		t.Errorf("Args[1] = %q", got.Args[1])
	}
	if got.Env["BASE"] != lp.Path {
		t.Errorf("Env BASE = %q, want %q", got.Env["BASE"], lp.Path)
	}
}

// ─── Load: errors + edge cases ────────────────────────────────

func TestLoad_nonexistentDir(t *testing.T) {
	_, err := Load("/this/does/not/exist/anywhere/ever")
	if err == nil {
		t.Fatal("want error")
	}
}

func TestLoad_fileNotDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	mustWrite(t, f, "")
	_, err := Load(f)
	if err == nil {
		t.Fatal("want error")
	}
}

// ─── safeJoin ─────────────────────────────────────────────────

func TestSafeJoin(t *testing.T) {
	cases := []struct {
		root    string
		rel     string
		wantErr bool
	}{
		{"/root", "child", false},
		{"/root", "a/b/c", false},
		{"/root", ".", false},
		{"/root", "", false},
		{"/root", "../escape", true},
		{"/root", "../../etc", true},
		{"/root", "a/../b", false},     // stays under root
		{"/root", "a/../../etc", true}, // escapes
		{"/root", "/abs/path", true},
	}
	for _, tc := range cases {
		_, err := safeJoin(tc.root, tc.rel)
		if (err != nil) != tc.wantErr {
			t.Errorf("safeJoin(%q, %q): wantErr=%v got %v", tc.root, tc.rel, tc.wantErr, err)
		}
	}
}
