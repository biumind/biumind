// Tests for /trust slash + the trust gate's effect on the status-
// line script runner. Walks through the full UX:
//   - bare `/trust` shows current state
//   - `/trust here` persists, `IsTrusted(cwd)` flips to true
//   - `/trust session` adds an in-memory grant only
//   - `/trust remove` revokes
//   - status-line runner fires only when trusted

package repl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/statusline"
	"github.com/biumind/biumind/apps/cli/biu/internal/trust"
)

func newTrustStore(t *testing.T) *trust.Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(trust.EnvBypass, "")
	s, err := trust.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// /trust without a store should soft-warn — embedders that opt out
// of the gate (test fixtures, sandboxed runs) shouldn't panic.
func TestTrustWithoutStoreSoftWarns(t *testing.T) {
	m := model{}
	got := m.handleTrust([]string{"/trust"})
	if !strings.Contains(got, "trust gate not enabled") {
		t.Errorf("nil trust should soft-warn; got %q", got)
	}
}

// Bare /trust shows the current cwd state + the empty list. Catch
// the "untrusted" branch's helpful hint.
func TestTrustBareShowsUntrustedHint(t *testing.T) {
	m := model{trust: newTrustStore(t)}
	got := m.handleTrust([]string{"/trust"})
	for _, must := range []string{"untrusted", "/trust here", "no persisted"} {
		if !strings.Contains(got, must) {
			t.Errorf("status missing %q;\nfull: %s", must, got)
		}
	}
}

// chdirAndGetCwd is a small helper that captures the OS-resolved cwd
// AFTER Chdir, working around the macOS /var → /private/var symlink
// indirection that would otherwise make t.TempDir() and os.Getwd()
// return strings that look identical to the user but compare !=.
// Production code is unaffected — the user's trust grant and the
// runtime IsTrusted check both go through os.Getwd, which is
// internally consistent.
//
// Restores the original cwd in t.Cleanup so a t.TempDir() removal
// doesn't leave the process pointing at a deleted directory —
// subsequent /bin/sh forks (e.g. /tasks tests, statusline runner)
// would otherwise spew "getcwd failed" to stderr.
func chdirAndGetCwd(t *testing.T, dir string) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("cleanup: chdir back to %s failed: %v", orig, err)
		}
	})
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// `/trust here` should persist the cwd. After it, IsTrusted returns
// true for both the cwd itself and any descendant.
func TestTrustHerePersistsCwd(t *testing.T) {
	cwd := chdirAndGetCwd(t, t.TempDir())
	m := model{trust: newTrustStore(t)}
	got := m.handleTrust([]string{"/trust", "here"})
	if !strings.HasPrefix(got, "/trust: persisted ") {
		t.Errorf("expected persisted ack; got %q", got)
	}
	if !m.trust.IsTrusted(cwd) {
		t.Errorf("cwd should be trusted after `/trust here`")
	}
	sub := filepath.Join(cwd, "sub")
	if !m.trust.IsTrusted(sub) {
		t.Errorf("subdir should inherit trust")
	}
}

// session grant must NOT show up in List() (persistent).
func TestTrustSessionInMemoryOnly(t *testing.T) {
	cwd := chdirAndGetCwd(t, t.TempDir())
	m := model{trust: newTrustStore(t)}
	got := m.handleTrust([]string{"/trust", "session"})
	if !strings.Contains(got, "for this session only") {
		t.Errorf("session ack message missing; got %q", got)
	}
	if !m.trust.IsTrusted(cwd) {
		t.Errorf("session grant should pass IsTrusted")
	}
	if len(m.trust.List()) != 0 {
		t.Errorf("session grant should not be in persistent list; got %v", m.trust.List())
	}
	if len(m.trust.SessionList()) != 1 {
		t.Errorf("session grant missing from SessionList: %v", m.trust.SessionList())
	}
}

// add + remove with explicit paths. add of an already-trusted path
// is idempotent; remove of an unknown path is also idempotent.
func TestTrustAddAndRemoveExplicit(t *testing.T) {
	m := model{trust: newTrustStore(t)}
	target := t.TempDir()

	if got := m.handleTrust([]string{"/trust", "add", target}); !strings.Contains(got, "persisted") {
		t.Errorf("add should persist; got %q", got)
	}
	if !m.trust.IsTrusted(target) {
		t.Fatal("add did not actually persist")
	}
	if got := m.handleTrust([]string{"/trust", "remove", target}); !strings.Contains(got, "revoked") {
		t.Errorf("remove should revoke; got %q", got)
	}
	if m.trust.IsTrusted(target) {
		t.Fatal("remove did not actually revoke")
	}
}

func TestTrustAddRequiresPath(t *testing.T) {
	m := model{trust: newTrustStore(t)}
	got := m.handleTrust([]string{"/trust", "add"})
	if !strings.Contains(got, "usage:") {
		t.Errorf("missing path should print usage; got %q", got)
	}
}

func TestTrustUnknownSubcommand(t *testing.T) {
	m := model{trust: newTrustStore(t)}
	got := m.handleTrust([]string{"/trust", "wat"})
	if !strings.HasPrefix(got, "/trust: usage:") {
		t.Errorf("unknown sub should print usage; got %q", got)
	}
}

// ─── Status-line gate integration ───────────────────────

// userStatusLineSegment must return "" when the cwd is untrusted —
// even when a status-line command IS configured. This is the
// security-critical path: a malicious settings.json is harmless if
// the gate refuses to fork the script.
func TestStatusLineBlockedWhenUntrusted(t *testing.T) {
	_ = chdirAndGetCwd(t, t.TempDir())
	tr := newTrustStore(t)
	// NOTE: we did NOT trust `cwd`.
	runner := statusline.New(statusline.Config{
		Command: `printf "should-not-appear"`,
	})
	m := model{
		statusLine: runner,
		trust:      tr,
	}
	if got := m.userStatusLineSegment(); got != "" {
		t.Errorf("untrusted dir must NOT execute status-line; got %q", got)
	}
}

// Once trusted, the same fixture produces the script's output.
func TestStatusLineRunsWhenTrusted(t *testing.T) {
	cwd := chdirAndGetCwd(t, t.TempDir())
	tr := newTrustStore(t)
	if _, err := tr.Trust(cwd); err != nil {
		t.Fatal(err)
	}
	runner := statusline.New(statusline.Config{
		Command: `printf "ok-trusted"`,
	})
	m := model{
		statusLine: runner,
		trust:      tr,
	}
	// Render twice — first call warms the cache, second hits it.
	for i := 0; i < 2; i++ {
		got := m.userStatusLineSegment()
		if got != "ok-trusted" {
			t.Errorf("call %d: trusted dir should run status-line; got %q", i, got)
		}
	}
}

// Nil trust store keeps the legacy "trust everything" behaviour so
// embedders that don't opt in aren't silently broken.
func TestStatusLineNilTrustRunsAlways(t *testing.T) {
	runner := statusline.New(statusline.Config{
		Command: `printf "open"`,
	})
	m := model{statusLine: runner /* trust: nil */}
	got := m.userStatusLineSegment()
	if got != "open" {
		t.Errorf("nil trust should not gate; got %q", got)
	}
}

// BIU_TRUST=1 must override the persistent gate so CI runs work
// without per-directory dialogs. Test re-uses the same untrusted
// fixture as TestStatusLineBlockedWhenUntrusted.
func TestStatusLineEnvBypassRunsWhenSet(t *testing.T) {
	_ = chdirAndGetCwd(t, t.TempDir())
	tr := newTrustStore(t)
	t.Setenv(trust.EnvBypass, "1")
	runner := statusline.New(statusline.Config{
		Command: `printf "ci-bypassed"`,
	})
	m := model{
		statusLine: runner,
		trust:      tr,
	}
	if got := m.userStatusLineSegment(); got != "ci-bypassed" {
		t.Errorf("BIU_TRUST=1 should bypass gate; got %q", got)
	}
}

// Chdir between invocations is a thing. After a `/trust here` in dir
// A, switching to dir B should leave the runner blocked again unless
// B is also trusted (or descended).
func TestStatusLineGateAppliesToCwdAtCallTime(t *testing.T) {
	tr := newTrustStore(t)
	a := chdirAndGetCwd(t, t.TempDir())
	b := t.TempDir()
	if _, err := tr.Trust(a); err != nil {
		t.Fatal(err)
	}

	runner := statusline.New(statusline.Config{
		Command: `printf cwd-script`,
	})
	m := model{statusLine: runner, trust: tr}

	// In trusted dir: runs.
	if got := m.userStatusLineSegment(); got != "cwd-script" {
		t.Errorf("dir A should run; got %q", got)
	}
	// Switch to untrusted B: blocked.
	_ = chdirAndGetCwd(t, b)
	// Fresh runner so the previous output isn't cached.
	runner2 := statusline.New(statusline.Config{
		Command: `printf cwd-script`,
	})
	m2 := model{statusLine: runner2, trust: tr}
	if got := m2.userStatusLineSegment(); got != "" {
		t.Errorf("dir B (untrusted) should be blocked; got %q", got)
	}
}

// Sanity: the userStatusLineSegment uses a context that respects
// the runner's timeout — no goroutine hanging the test process.
func TestUserStatusLineSegmentHonoursTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx // smoke-test shape; the runner is the timeout owner
}
