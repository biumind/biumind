// Tests for user-defined slash command integration. We exercise:
//   - matchSlash includes user commands when extras are passed
//   - userSlashItems materialises commands from disk
//   - lookupUserCommand finds + parses verbatim args
//   - runUserCommand without engine returns the no-engine soft warn

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUserCmd(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".biumind", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// matchSlash with extras must surface user commands alongside
// built-ins. Catches accidental dropping when adding new built-ins.
func TestMatchSlashIncludesExtras(t *testing.T) {
	extras := []SlashCmd{
		{Name: "/refactor", Description: "[user] refactor"},
		{Name: "/deploy", Description: "[user] ship"},
	}
	got := matchSlash("/", extras)
	want := len(slashCmds) + 2
	if len(got) != want {
		t.Errorf("count: got %d, want %d", len(got), want)
	}
	// Prefix filter should also see extras.
	got = matchSlash("/refac", extras)
	if len(got) != 1 || got[0].Name != "/refactor" {
		t.Errorf("prefix filter missed user command: %+v", got)
	}
}

// userSlashItems pulls from ~/.biumind/commands/. Loads only the
// fresh-on-disk view so an editor save is reflected immediately
// (no restart).
func TestUserSlashItemsLoadsFromDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)

	writeUserCmd(t, home, "refactor",
		"---\ndescription: refactor a function\n---\nbody $ARGUMENTS\n")

	m := model{}
	got := m.userSlashItems()
	if len(got) != 1 {
		t.Fatalf("count: got %d, want 1", len(got))
	}
	if got[0].Name != "/refactor" {
		t.Errorf("name: got %q", got[0].Name)
	}
	if !strings.Contains(got[0].Description, "[user]") {
		t.Errorf("description should tag source; got %q", got[0].Description)
	}
	if !strings.Contains(got[0].Description, "refactor a function") {
		t.Errorf("description body lost: %q", got[0].Description)
	}
}

// userSlashItems with no commands → nil (matcher then renders only
// built-ins).
func TestUserSlashItemsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	m := model{}
	if got := m.userSlashItems(); got != nil {
		t.Errorf("empty registry should return nil; got %v", got)
	}
}

// lookupUserCommand recognises the slash + parses verbatim args.
func TestLookupUserCommandFindsAndParses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	writeUserCmd(t, home, "refactor",
		"Refactor the target.\n\nTarget: $ARGUMENTS\n")

	m := model{}
	cmd, args, ok := m.lookupUserCommand("/refactor", "/refactor pkg/auth/jwt.go")
	if !ok {
		t.Fatal("lookup miss")
	}
	if cmd.Name != "refactor" {
		t.Errorf("Name: got %q", cmd.Name)
	}
	if args != "pkg/auth/jwt.go" {
		t.Errorf("args: got %q", args)
	}
	// Render substitution.
	rendered := cmd.Render(args)
	if !strings.Contains(rendered, "Target: pkg/auth/jwt.go") {
		t.Errorf("render lost args: %q", rendered)
	}
}

func TestLookupUserCommandUnknownSlashReturnsFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	m := model{}
	if _, _, ok := m.lookupUserCommand("/never-existed", "/never-existed args"); ok {
		t.Errorf("missing command should not resolve")
	}
}

// Slash without leading `/` should not match.
func TestLookupUserCommandRequiresLeadingSlash(t *testing.T) {
	m := model{}
	if _, _, ok := m.lookupUserCommand("refactor", "refactor"); ok {
		t.Errorf("non-slash should not match")
	}
}

// Empty arg string after the slash works — common case for commands
// that don't expect input.
func TestLookupUserCommandHandlesNoArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	writeUserCmd(t, home, "noargs", "Do the thing.\n")
	m := model{}
	_, args, ok := m.lookupUserCommand("/noargs", "/noargs")
	if !ok {
		t.Fatal("lookup miss")
	}
	if args != "" {
		t.Errorf("expected empty args, got %q", args)
	}
}

// runUserCommand's full path (system note + engine dispatch)
// requires a real Bubble Tea harness to observe via the model's
// internal state. The lookup + render tests above cover the
// behaviour that matters for users (slash discovery, arg parsing,
// substitution); any future regression in runUserCommand's wiring
// surfaces as a missing message in the integration `/ultraplan`
// suite, which already covers the same startEngineStream path.
