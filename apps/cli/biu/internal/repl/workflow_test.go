// Tests for /workflow slash. Covers list / show / dispatch error
// paths. The full dispatch (engine streaming) requires a Bubble
// Tea harness — same caveat as /ultraplan / user-command tests; we
// rely on the engine + workflows unit tests to cover the runtime.

package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflowFile(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".biumind", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// /workflow on an empty registry should explain the file path
// users would write to.
func TestRenderWorkflowListEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirTmp(t)
	m := model{}
	got := m.renderWorkflowList()
	if !strings.Contains(got, "no workflows defined") {
		t.Errorf("empty list should explain emptiness; got %q", got)
	}
	if !strings.Contains(got, "~/.biumind/workflows/") {
		t.Errorf("empty list should mention the dir; got %q", got)
	}
}

// /workflow with workflows on disk lists them with source +
// requires + description.
func TestRenderWorkflowListShowsRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	writeWorkflowFile(t, home, "ship",
		"---\ndescription: Ship a feature end-to-end\nrequires: git_repo\n---\nbody")
	writeWorkflowFile(t, home, "review-pr",
		"---\ndescription: Review the current PR\n---\nbody")

	m := model{}
	got := m.renderWorkflowList()
	for _, must := range []string{
		"workflows (2):",
		"/workflow ship",
		"[user]",
		"requires=git_repo",
		"Ship a feature end-to-end",
		"/workflow review-pr",
		"Review the current PR",
		"preview: /workflow show",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("list missing %q;\nfull:\n%s", must, got)
		}
	}
}

// /workflow show <name> emits frontmatter summary + the rendered
// body with $ARGUMENTS replaced by `<args>` placeholder.
func TestRenderWorkflowShow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTmp(t)
	writeWorkflowFile(t, home, "ship",
		"---\n"+
			"description: Ship a feature\n"+
			"requires: git_repo\n"+
			"args: feature\n"+
			"---\n"+
			"## Plan: $ARGUMENTS\n\n1. /ultraplan implement: $ARGUMENTS\n")
	m := model{}
	got := m.renderWorkflowShow("ship")
	for _, must := range []string{
		"workflow: ship [user]",
		"description: Ship a feature",
		"path:",
		"requires:    git_repo",
		"args:        feature",
		"--- body (rendered with $ARGUMENTS=<args>) ---",
		"Plan: <args>",
		"implement: <args>",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("show missing %q;\nfull:\n%s", must, got)
		}
	}
}

func TestRenderWorkflowShowUnknownName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirTmp(t)
	m := model{}
	got := m.renderWorkflowShow("does-not-exist")
	if !strings.Contains(got, "no workflow") {
		t.Errorf("unknown name should report; got %q", got)
	}
}

// /workflow show without a name shows usage rather than panicking.
// We can't observe the system note from the Bubble Tea handler
// directly; the lookup test path covers the underlying logic, and
// the slash dispatcher's `len(parts) < 3` branch is straightforward.

// /workflow <name> with a registry that lacks the workflow falls
// through to a clear error. We use the engine-less guard path
// (engine == nil → "requires engine path") to exercise the
// fall-through cheaply.
func TestHandleWorkflowWithoutEngineWarns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirTmp(t)
	m := model{}
	// Run through the dispatch surface; we can't easily intercept
	// the system note, but the engine-nil guard returns early so
	// no panic / state mutation is the contract under test.
	_, _ = m.handleWorkflow([]string{"/workflow", "ship"}, "/workflow ship")
}
