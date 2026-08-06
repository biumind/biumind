package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

func TestPersist_AddDirectoriesLocal(t *testing.T) {
	cwd := t.TempDir()
	err := PersistPermissionUpdate(cwd, &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/foo", "/tmp/bar"},
		Destination: sdkproto.PermissionDestLocalSettings,
	})
	if err != nil {
		t.Fatalf("persist add: %v", err)
	}
	got := readSettings(t, filepath.Join(cwd, ".biumind", "settings.local.json"))
	if got.Permissions == nil {
		t.Fatal("Permissions block missing after persist")
	}
	want := []string{"/tmp/foo", "/tmp/bar"}
	if !equalStrings(got.Permissions.AdditionalDirectories, want) {
		t.Errorf("got %+v, want %+v", got.Permissions.AdditionalDirectories, want)
	}
}

func TestPersist_AddDirectoriesIdempotent(t *testing.T) {
	cwd := t.TempDir()
	add := &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/foo"},
		Destination: sdkproto.PermissionDestLocalSettings,
	}
	for i := 0; i < 3; i++ {
		if err := PersistPermissionUpdate(cwd, add); err != nil {
			t.Fatalf("persist iter %d: %v", i, err)
		}
	}
	got := readSettings(t, filepath.Join(cwd, ".biumind", "settings.local.json"))
	if len(got.Permissions.AdditionalDirectories) != 1 {
		t.Errorf("expected dedup; got %+v", got.Permissions.AdditionalDirectories)
	}
}

func TestPersist_RemoveDirectories(t *testing.T) {
	cwd := t.TempDir()
	if err := PersistPermissionUpdate(cwd, &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/foo", "/tmp/bar"},
		Destination: sdkproto.PermissionDestLocalSettings,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := PersistPermissionUpdate(cwd, &sdkproto.RemoveDirectories{
		Type:        sdkproto.PermissionUpdateRemoveDirectories,
		Directories: []string{"/tmp/foo"},
		Destination: sdkproto.PermissionDestLocalSettings,
	}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := readSettings(t, filepath.Join(cwd, ".biumind", "settings.local.json"))
	want := []string{"/tmp/bar"}
	if !equalStrings(got.Permissions.AdditionalDirectories, want) {
		t.Errorf("got %+v, want %+v", got.Permissions.AdditionalDirectories, want)
	}
}

func TestPersist_ProjectDestinationCreatesParent(t *testing.T) {
	cwd := t.TempDir()
	err := PersistPermissionUpdate(cwd, &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/srv/x"},
		Destination: sdkproto.PermissionDestProjectSettings,
	})
	if err != nil {
		t.Fatalf("persist project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".biumind", "settings.json")); err != nil {
		t.Errorf(".biumind/settings.json should exist; %v", err)
	}
}

func TestPersist_SessionDestinationIsErrSessionDest(t *testing.T) {
	cwd := t.TempDir()
	err := PersistPermissionUpdate(cwd, &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/foo"},
		Destination: sdkproto.PermissionDestSession,
	})
	if !errors.Is(err, ErrSessionDest) {
		t.Errorf("session destination should return ErrSessionDest; got %v", err)
	}
}

func TestPersist_CliArgDestinationIsErrSessionDest(t *testing.T) {
	cwd := t.TempDir()
	err := PersistPermissionUpdate(cwd, &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/foo"},
		Destination: sdkproto.PermissionDestCliArg,
	})
	if !errors.Is(err, ErrSessionDest) {
		t.Errorf("cliArg destination should return ErrSessionDest; got %v", err)
	}
}

func TestPersist_LocalRequiresCwd(t *testing.T) {
	err := PersistPermissionUpdate("", &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/foo"},
		Destination: sdkproto.PermissionDestLocalSettings,
	})
	if !errors.Is(err, ErrCwdRequired) {
		t.Errorf("empty cwd should return ErrCwdRequired; got %v", err)
	}
}

func TestPersist_AtomicLeavesNoTempFiles(t *testing.T) {
	cwd := t.TempDir()
	if err := PersistPermissionUpdate(cwd, &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/foo"},
		Destination: sdkproto.PermissionDestLocalSettings,
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(cwd, ".biumind"))
	for _, e := range entries {
		name := e.Name()
		if name == "settings.local.json" {
			continue
		}
		t.Errorf("unexpected leftover file: %s", name)
	}
}

func TestPersist_AddRulesAndPersists(t *testing.T) {
	cwd := t.TempDir()
	err := PersistPermissionUpdate(cwd, &sdkproto.AddRules{
		Type: sdkproto.PermissionUpdateAddRules,
		Rules: []sdkproto.PermissionRuleValue{
			{ToolName: "Bash", RuleContent: "git:*"},
		},
		Behavior:    sdkproto.PermissionAllow,
		Destination: sdkproto.PermissionDestLocalSettings,
	})
	if err != nil {
		t.Fatalf("persist addRules: %v", err)
	}
	got := readSettings(t, filepath.Join(cwd, ".biumind", "settings.local.json"))
	if len(got.Permissions.Allow) != 1 || got.Permissions.Allow[0] != "Bash(git:*)" {
		t.Errorf("Allow rules: %+v", got.Permissions.Allow)
	}
}

func TestPersist_SetMode(t *testing.T) {
	cwd := t.TempDir()
	err := PersistPermissionUpdate(cwd, &sdkproto.SetModeUpdate{
		Type:        sdkproto.PermissionUpdateSetMode,
		Mode:        sdkproto.PermissionModeAcceptEdits,
		Destination: sdkproto.PermissionDestLocalSettings,
	})
	if err != nil {
		t.Fatalf("persist setMode: %v", err)
	}
	got := readSettings(t, filepath.Join(cwd, ".biumind", "settings.local.json"))
	if got.Permissions.DefaultMode != sdkproto.PermissionModeAcceptEdits {
		t.Errorf("DefaultMode = %s", got.Permissions.DefaultMode)
	}
}

func TestSupportsPersistence(t *testing.T) {
	cases := []struct {
		dest string
		want bool
	}{
		{sdkproto.PermissionDestUserSettings, true},
		{sdkproto.PermissionDestProjectSettings, true},
		{sdkproto.PermissionDestLocalSettings, true},
		{sdkproto.PermissionDestSession, false},
		{sdkproto.PermissionDestCliArg, false},
		{"unknown", false},
	}
	for _, c := range cases {
		if got := SupportsPersistence(c.dest); got != c.want {
			t.Errorf("SupportsPersistence(%q)=%v want %v", c.dest, got, c.want)
		}
	}
}

// ─── helpers ──────────────────────────────────────────

func readSettings(t *testing.T, path string) *Settings {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return &s
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
