// Tests for /agents + /agents create handlers. The scaffold engine
// itself has full coverage in internal/agents/scaffold_test.go;
// here we focus on the REPL-side flag parsing + the "after-write
// status line" UX.

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsListEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	m := model{}
	got := m.handleAgents([]string{"/agents"})
	if !strings.Contains(got, "general-purpose") {
		t.Errorf("list should always advertise general-purpose; got %q", got)
	}
	// User has no .md files but built-ins are seeded so we don't
	// hit the "no custom agents" line.
	if !strings.Contains(got, "registered sub-agent types") {
		t.Errorf("list should print the heading; got %q", got)
	}
}

func TestAgentsCreateMissingNameShowsUsage(t *testing.T) {
	m := model{}
	got := m.handleAgents([]string{"/agents", "create"})
	if !strings.Contains(got, "usage:") {
		t.Errorf("missing name should show usage; got %q", got)
	}
}

func TestAgentsCreateUserScopeWritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{}
	got := m.handleAgents([]string{"/agents", "create", "myhelper"})
	if !strings.HasPrefix(got, "/agents: wrote") {
		t.Errorf("status line should ack write; got %q", got)
	}
	want := filepath.Join(home, ".biumind", "agents", "myhelper.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("file not written: %v", err)
	}
}

func TestAgentsCreateProjectScopeWritesUnderCwd(t *testing.T) {
	cwd := chdirTmp(t)
	m := model{}
	got := m.handleAgents([]string{
		"/agents", "create", "team-agent", "--scope", "project",
	})
	if !strings.Contains(got, "wrote") {
		t.Errorf("status line should ack write; got %q", got)
	}
	want := filepath.Join(cwd, ".biumind", "agents", "team-agent.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("project-scope file missing: %v", err)
	}
}

func TestAgentsCreatePresetThreadsThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{}
	got := m.handleAgents([]string{
		"/agents", "create", "my-review", "--from", "review",
	})
	if !strings.Contains(got, "preset=review") {
		t.Errorf("status line should reflect preset; got %q", got)
	}
	body, _ := os.ReadFile(
		filepath.Join(home, ".biumind", "agents", "my-review.md"))
	if !strings.Contains(string(body), "BLOCKER") {
		t.Errorf("review preset body should ship severity vocab; got %q", body)
	}
}

// Reserved built-in name should be rejected with a clear message.
func TestAgentsCreateRejectsReservedName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{}
	got := m.handleAgents([]string{"/agents", "create", "Plan"})
	if !strings.Contains(got, "collides with a built-in") {
		t.Errorf("status line should explain reserved-name collision; got %q", got)
	}
}

// --force needed to overwrite. Without it, the second create fails
// with the documented hint.
func TestAgentsCreateRefusesOverwriteWithoutForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{}
	if got := m.handleAgents([]string{"/agents", "create", "x"}); !strings.Contains(got, "wrote") {
		t.Fatalf("first create should succeed; got %q", got)
	}
	got := m.handleAgents([]string{"/agents", "create", "x"})
	if !strings.Contains(got, "Re-run with --force") {
		t.Errorf("second create should refuse with --force hint; got %q", got)
	}
}

func TestAgentsCreateForceOverwrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{}
	_ = m.handleAgents([]string{"/agents", "create", "x"})
	got := m.handleAgents([]string{"/agents", "create", "x", "--force"})
	if !strings.Contains(got, "overwrote") {
		t.Errorf("status line should say overwrote; got %q", got)
	}
}

func TestAgentsCreateUnknownFlagFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := model{}
	got := m.handleAgents([]string{"/agents", "create", "x", "--bogus"})
	if !strings.Contains(got, "unknown flag") {
		t.Errorf("unknown flag should fail; got %q", got)
	}
}

// --scope without value should print a useful error rather than
// silently swallowing the next argument.
func TestAgentsCreateScopeWithoutValue(t *testing.T) {
	m := model{}
	got := m.handleAgents([]string{"/agents", "create", "x", "--scope"})
	if !strings.Contains(got, "--scope requires") {
		t.Errorf("missing scope value should explain; got %q", got)
	}
}

// --from without value, same shape.
func TestAgentsCreateFromWithoutValue(t *testing.T) {
	m := model{}
	got := m.handleAgents([]string{"/agents", "create", "x", "--from"})
	if !strings.Contains(got, "--from requires") {
		t.Errorf("missing preset value should explain; got %q", got)
	}
}

// After scaffolding, the next /agents list call should pick up the
// new agent — proves the scaffolder's frontmatter is parser-
// compatible at the REPL level (not just the unit tests).
func TestAgentsListSeesScaffoldedAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	m := model{}
	if got := m.handleAgents([]string{"/agents", "create", "round-trip"}); !strings.Contains(got, "wrote") {
		t.Fatalf("create failed: %q", got)
	}
	listing := m.handleAgents([]string{"/agents"})
	if !strings.Contains(listing, "round-trip") {
		t.Errorf("listing should pick up scaffolded agent; got %q", listing)
	}
	if !strings.Contains(listing, "[user]") {
		t.Errorf("listing should mark scaffolded agent as user-source; got %q", listing)
	}
}
