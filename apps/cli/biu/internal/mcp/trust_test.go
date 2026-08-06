// Tests for Bootstrap's trust gate. Project-source entries get
// gated; user-source entries always pass. SkipNotifier fires per
// blocked entry.

package mcp

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeTrust returns a configurable bool. Tests flip it to verify
// the gate flips behaviour mid-test.
type fakeTrust struct {
	trusted atomic.Bool
}

func (f *fakeTrust) IsTrustedNow() bool { return f.trusted.Load() }

// fakeRegistry replaces the real Connect path so tests don't try to
// fork actual MCP servers. We just record what would have been
// connected.
type connectRecord struct {
	mu       sync.Mutex
	attempts []string
}

func (c *connectRecord) record(name string) {
	c.mu.Lock()
	c.attempts = append(c.attempts, name)
	c.mu.Unlock()
}

func (c *connectRecord) seen(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.attempts {
		if n == name {
			return true
		}
	}
	return false
}

// We can't easily intercept Connect without restructuring the
// registry, so the trust-gate tests use commands that fail to start
// (`/nonexistent/binary`) — Bootstrap reaches Connect, gets a
// process-spawn failure, and records it as Err. The TrustBlocked
// flag fires BEFORE the spawn attempt, which is exactly the
// distinguishing signal we test for.

// Distinct fake binaries per entry so the dedupe pass keeps both —
// dedupe runs BEFORE the trust gate, so identical commands would
// collapse to one entry and skip the gate entirely.
const fakeUserBin = "/no/such/binary-for-test-user-9999"
const fakeProjBin = "/no/such/binary-for-test-proj-9999"

func bootstrapTwoEntries(t *testing.T, trustOK bool, gate TrustGate) []BootstrapResult {
	t.Helper()
	reg := NewRegistry()
	t.Cleanup(func() { _ = reg.Close() })

	inputs := []BootstrapInput{
		{Source: "manual", Name: "user-server", Command: fakeUserBin},
		{Source: "project", Name: "proj-server", Command: fakeProjBin},
	}
	skipped := []string{}
	results := reg.BootstrapWithOptions(
		context.Background(),
		inputs,
		BootstrapOptions{
			StderrSink: func(string) {},
			TrustGate:  gate,
			SkipNotifier: func(name string) {
				skipped = append(skipped, name)
			},
		},
	)
	_ = trustOK
	_ = skipped
	return results
}

// Without a gate, both entries reach Connect — both get spawn
// errors (the binary doesn't exist) but neither is TrustBlocked.
func TestBootstrapWithoutGateSpawnsBoth(t *testing.T) {
	res := bootstrapTwoEntries(t, true, nil)
	for _, r := range res {
		if r.TrustBlocked {
			t.Errorf("no gate set; entry %s should not be TrustBlocked", r.Name)
		}
	}
}

// Untrusted gate: project entry is TrustBlocked, user entry still
// reaches Connect.
func TestBootstrapUntrustedGateBlocksProjectEntry(t *testing.T) {
	gate := &fakeTrust{} // default: not trusted
	res := bootstrapTwoEntries(t, false, gate)

	var userRes, projRes *BootstrapResult
	for i := range res {
		switch res[i].Name {
		case "user-server":
			userRes = &res[i]
		case "proj-server":
			projRes = &res[i]
		}
	}
	if userRes == nil || projRes == nil {
		t.Fatalf("results missing entries: %+v", res)
	}
	if userRes.TrustBlocked {
		t.Errorf("user-source entry should NEVER be trust-blocked")
	}
	if !projRes.TrustBlocked {
		t.Errorf("project-source entry should be trust-blocked when gate untrusted; got %+v", projRes)
	}
	// Project entry's Err should be nil (we short-circuited before
	// the spawn attempt). User entry's Err should be set (binary
	// doesn't exist).
	if projRes.Err != nil {
		t.Errorf("trust-blocked entry should not have spawn err; got %v", projRes.Err)
	}
	if userRes.Err == nil {
		t.Errorf("user entry should have failed spawn (fake binary); got nil err")
	}
}

// Trusted gate: project entry passes through to Connect (and fails
// because the fake binary doesn't exist — but for the gate's
// purposes that's success).
func TestBootstrapTrustedGateAllowsProjectEntry(t *testing.T) {
	gate := &fakeTrust{}
	gate.trusted.Store(true)
	res := bootstrapTwoEntries(t, true, gate)
	for _, r := range res {
		if r.TrustBlocked {
			t.Errorf("trusted gate should not block any entry; got %+v", r)
		}
	}
}

// Trust flip mid-Bootstrap is tested implicitly by the Bootstrap-
// level cache (gate is sampled once at the start). What we lock
// here is the same call returning consistent results across two
// runs when the gate state changes.
func TestBootstrapHonoursLatestGateValue(t *testing.T) {
	gate := &fakeTrust{}

	// Run 1: untrusted.
	res := bootstrapTwoEntries(t, false, gate)
	for _, r := range res {
		if r.Name == "proj-server" && !r.TrustBlocked {
			t.Errorf("first run: project should be blocked")
		}
	}

	// Run 2: trusted. Same store / same inputs, gate flipped.
	gate.trusted.Store(true)
	res = bootstrapTwoEntries(t, true, gate)
	for _, r := range res {
		if r.TrustBlocked {
			t.Errorf("second run: trusted gate should let everything through; got %+v", r)
		}
	}
}

// SkipNotifier fires once per blocked entry with the entry's name
// — wiring layer can render "1 server blocked: <name>".
func TestBootstrapSkipNotifierFiresPerBlockedEntry(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()
	gate := &fakeTrust{} // untrusted
	var blocked []string
	var mu sync.Mutex
	results := reg.BootstrapWithOptions(
		context.Background(),
		[]BootstrapInput{
			{Source: "project", Name: "p1", Command: "/no/such/p1"},
			{Source: "project", Name: "p2", Command: "/no/such/p2"},
			{Source: "manual", Name: "m1", Command: "/no/such/m1"},
		},
		BootstrapOptions{
			StderrSink: func(string) {},
			TrustGate:  gate,
			SkipNotifier: func(name string) {
				mu.Lock()
				blocked = append(blocked, name)
				mu.Unlock()
			},
		},
	)
	mu.Lock()
	defer mu.Unlock()
	if len(blocked) != 2 {
		t.Errorf("notifier fired %d times, want 2; got blocked=%v", len(blocked), blocked)
	}
	for _, n := range []string{"p1", "p2"} {
		found := false
		for _, b := range blocked {
			if b == n {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("notifier missed entry %q; got %v", n, blocked)
		}
	}
	// Sanity: results carry the same flag.
	for _, r := range results {
		switch r.Name {
		case "p1", "p2":
			if !r.TrustBlocked {
				t.Errorf("%s should have TrustBlocked=true", r.Name)
			}
		case "m1":
			if r.TrustBlocked {
				t.Errorf("manual entry incorrectly marked TrustBlocked")
			}
		}
	}
}

// Legacy 3-arg Bootstrap call must keep working without trust.
// Catches accidental breaking changes when adding new options.
func TestLegacyBootstrapStillWorksWithoutOptions(t *testing.T) {
	reg := NewRegistry()
	defer reg.Close()
	var captured strings.Builder
	results := reg.Bootstrap(
		context.Background(),
		[]BootstrapInput{
			{Source: "manual", Name: "x", Command: "/no/such/legacy-bin"},
		},
		func(line string) { captured.WriteString(line) },
	)
	if len(results) != 1 {
		t.Fatalf("legacy Bootstrap should return 1 result; got %d", len(results))
	}
	if results[0].TrustBlocked {
		t.Errorf("legacy entry point should never set TrustBlocked")
	}
}
