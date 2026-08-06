// Tests for the sandbox-related additions to BashTool: the
// dangerously_disable_sandbox per-call escape hatch and the
// sandbox section in Description that surfaces active fs
// allow/deny lists. Pairs with internal/sandbox/layered_test.go;
// that suite covers the sandbox layer itself, this one covers the
// integration through the tool.

package web

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

// newPermCtx returns an empty Context for tests. Helper kept tiny so
// each test reads top-down without scrolling.
func newPermCtx(t *testing.T) *permissions.Context {
	t.Helper()
	return permissions.NewContext()
}

// dangerously_disable_sandbox=true must change the [sandbox=…]
// header in the result body to "off-disabled" so the model can see
// the bypass took effect (separate from the helper-not-found "off"
// fallback).
func TestBashDisableSandboxLabel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := BashTool{}
	out, _ := tool.Call(context.Background(), map[string]any{
		"command":                     "echo ok",
		"dangerously_disable_sandbox": true,
	}, &engine.ToolEnv{})
	body := flattenBg(out)
	if !strings.Contains(body, "[sandbox=off-disabled") {
		t.Errorf("expected off-disabled label; got:\n%s", body)
	}
}

// Without the flag, the body shows the platform-default sandbox
// label (macos-sandbox / bwrap / off). Either way it must NOT show
// off-disabled.
func TestBashDefaultSandboxLabel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := BashTool{}
	out, _ := tool.Call(context.Background(), map[string]any{
		"command": "echo ok",
	}, &engine.ToolEnv{})
	body := flattenBg(out)
	if strings.Contains(body, "off-disabled") {
		t.Errorf("default call should not show off-disabled: %s", body)
	}
	// Some valid label MUST appear.
	if !strings.Contains(body, "[sandbox=") {
		t.Errorf("missing sandbox label: %s", body)
	}
}

// Description for a zero-value BashTool announces default policy +
// network-blocked-by-default.
func TestBashDescriptionDefaultSandbox(t *testing.T) {
	got := BashTool{}.Description(nil)
	for _, want := range []string{
		"# Sandbox",
		"writes restricted to project cwd",
		"Network: blocked by default",
		"dangerously_disable_sandbox",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing %q\n--- full ---\n%s", want, got)
		}
	}
}

// Description for a configured BashTool reflects each layered list.
func TestBashDescriptionConfiguredSandbox(t *testing.T) {
	got := BashTool{
		FSReadDeny:             []string{"/Users/me/.ssh"},
		FSReadAllowWithinDeny:  []string{"/Users/me/.ssh/known_hosts"},
		FSWriteAllowExtra:      []string{"/srv/output"},
		FSWriteDenyWithinAllow: []string{"/srv/output/secret"},
		AllowNetworkByDefault:  true,
	}.Description(nil)
	for _, want := range []string{
		"Read-blocked paths: /Users/me/.ssh",
		"Read-allowed within blocks: /Users/me/.ssh/known_hosts",
		"Extra writable roots: /srv/output",
		"Write-blocked within allows: /srv/output/secret",
		"Network: allowed by default",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing %q\n--- full ---\n%s", want, got)
		}
	}
}

// dangerously_disable_sandbox=true must let a write outside cwd
// actually land — verifying the bypass takes effect end-to-end.
// We write into the test's TempDir, which IS already inside our
// "writable" set on macOS (TempDir lives under /private/var/folders),
// so to genuinely test the bypass we target /tmp instead — that's
// always under sandbox-exec's allow list too. Instead, the
// behavioural assertion is "the command exit was 0" rather than
// "the file landed", because covering "write to a forbidden path
// succeeds" requires choosing a path the test runner's user has
// perms to write to but the sandbox blocks. /private/etc is
// owned by root so a non-root test can't prove the bypass works
// there. The integration test stays at the label-check level;
// behavioural sandbox bypass coverage is in
// sandbox/layered_test.go::TestWrapDisableSkipsSandbox.
func TestBashDisableSandboxRunsCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := BashTool{}
	out, _ := tool.Call(context.Background(), map[string]any{
		"command":                     "echo ok",
		"dangerously_disable_sandbox": true,
	}, &engine.ToolEnv{})
	if out.IsError {
		t.Fatalf("disabled-sandbox echo errored: %+v", out)
	}
	if !strings.Contains(flattenBg(out), "ok") {
		t.Errorf("output missing 'ok': %s", flattenBg(out))
	}
}

func TestBashEffectiveWriteAllowExtra_NilCtx(t *testing.T) {
	tool := BashTool{FSWriteAllowExtra: []string{"/tmp/static"}}
	got := tool.effectiveWriteAllowExtra()
	if len(got) != 1 || got[0] != "/tmp/static" {
		t.Errorf("nil ctx must return static set; got %+v", got)
	}
}

func TestBashEffectiveWriteAllowExtra_UnionDedup(t *testing.T) {
	c := newPermCtx(t)
	c.AddDirectories(permissions.SrcSession, []string{"/tmp/dyn-a", "/tmp/static"}) // overlap
	tool := BashTool{
		FSWriteAllowExtra: []string{"/tmp/static", "/tmp/static-only"},
		PermCtx:           c,
	}
	got := tool.effectiveWriteAllowExtra()
	want := map[string]bool{
		"/tmp/static":      true,
		"/tmp/static-only": true,
		"/tmp/dyn-a":       true,
	}
	if len(got) != len(want) {
		t.Errorf("dedup wrong: got %+v want keys %+v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected entry: %s", p)
		}
	}
}

func TestBashEffectiveWriteAllowExtra_DynamicAddPropagates(t *testing.T) {
	c := newPermCtx(t)
	tool := BashTool{PermCtx: c}
	if got := tool.effectiveWriteAllowExtra(); len(got) != 0 {
		t.Errorf("empty ctx → empty list; got %+v", got)
	}
	c.AddDirectories(permissions.SrcSession, []string{"/tmp/late-add"})
	if got := tool.effectiveWriteAllowExtra(); len(got) != 1 || got[0] != "/tmp/late-add" {
		t.Errorf("late add should appear; got %+v", got)
	}
}

func TestBashSandboxSection_ShowsCtxDirs(t *testing.T) {
	c := newPermCtx(t)
	c.AddDirectories(permissions.SrcSession, []string{"/tmp/dyn"})
	tool := BashTool{PermCtx: c}
	desc := tool.Description(nil)
	if !strings.Contains(desc, "/tmp/dyn") {
		t.Errorf("description should list ctx-sourced dirs; got:\n%s", desc)
	}
}
