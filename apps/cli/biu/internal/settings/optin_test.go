// Tests for the auto-mode opt-in marker (optin.go).
//
// The marker gates unattended tool execution, so the load-bearing
// property is LAYER SCOPE: only ~/.biumind/settings.json counts.
// These tests pin that a project/local copy is never honoured and
// that ApplyToContext doesn't consume the field either.

package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

func TestAutoModeOptedIn_MissingFileIsFalse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if AutoModeOptedIn() {
		t.Error("no user settings file → not opted in")
	}
}

func TestSetAutoModeOptIn_WritesUserLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SetAutoModeOptIn(); err != nil {
		t.Fatalf("SetAutoModeOptIn: %v", err)
	}
	if !AutoModeOptedIn() {
		t.Error("marker should be visible after SetAutoModeOptIn")
	}

	raw, err := os.ReadFile(filepath.Join(home, ".biumind", "settings.json"))
	if err != nil {
		t.Fatalf("user settings file missing: %v", err)
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Permissions == nil || s.Permissions.AutoModeOptIn == nil || !*s.Permissions.AutoModeOptIn {
		t.Errorf("file should carry permissions.autoModeOptIn=true; got %+v", s.Permissions)
	}
}

// A project-layer settings.json carrying autoModeOptIn=true must NOT
// count — AutoModeOptedIn doesn't even take a cwd. This is the
// malicious-repository guard: a repo cannot pre-accept auto mode on
// the user's behalf.
func TestAutoModeOptedIn_ProjectLayerNeverCounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Seed a project-style file with the marker set. Even if code
	// somewhere pointed a project loader at it, AutoModeOptedIn must
	// stay false until the USER layer opts in.
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".biumind"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"permissions":{"autoModeOptIn":true}}`)
	if err := os.WriteFile(filepath.Join(project, ".biumind", "settings.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	localBody := []byte(`{"permissions":{"autoModeOptIn":true}}`)
	if err := os.WriteFile(filepath.Join(project, ".biumind", "settings.local.json"), localBody, 0o644); err != nil {
		t.Fatal(err)
	}

	if AutoModeOptedIn() {
		t.Error("project/local layers must never satisfy the opt-in check")
	}
}

// ApplyToContext must not consume autoModeOptIn — the field is a
// REPL-level UX marker, not a permission input. A user layer with the
// marker set and nothing else must leave mode + guards at defaults.
func TestApplyToContext_IgnoresAutoModeOptIn(t *testing.T) {
	on := true
	layered := &Layered{
		User: &Settings{Permissions: &PermissionsBlock{AutoModeOptIn: &on}},
	}
	ctx := permissions.NewContext()
	if mode := layered.ApplyToContext(ctx); mode != permissions.ModeDefault {
		t.Errorf("autoModeOptIn must not influence mode resolution; got %q", mode)
	}
	if ctx.Mode() != permissions.ModeDefault {
		t.Errorf("ctx mode should stay default; got %q", ctx.Mode())
	}
	if !ctx.GuardsEnabled() {
		t.Error("guards should stay at their default (on)")
	}
}
