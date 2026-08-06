// Tests for the trust-gate plumbing on hooks.Registry. Covers:
//
//   - SetTrustGate(nil) keeps legacy "everything fires" behaviour.
//   - Untrusted gate causes For() to return nil + Has() to return false.
//   - SkipNotifier fires once per blocked event (and includes the count
//     so the wiring layer can render "5 PreToolUse hooks skipped").
//   - Mid-session trust flip (untrusted → trusted) takes effect on
//     the next lookup with no Registry rebuild.

package hooks

import (
	"encoding/json"
	"sync/atomic"
	"testing"
)

// stubGate is a minimal TrustGate whose answer is controlled via
// an atomic int so tests can flip trust mid-flight without races.
type stubGate struct {
	trusted atomic.Bool
}

func (s *stubGate) IsTrustedNow() bool { return s.trusted.Load() }

// helper: build a registry with one PreToolUse hook (for tests
// that exercise For/Has).
func registryWithOneHook(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	raw := json.RawMessage(`[{"matcher":"Bash","hooks":[{"type":"command","command":"true"}]}]`)
	r.Add("test", map[string][]json.RawMessage{
		string(EventPreToolUse): {raw},
	})
	return r
}

func TestRegistryNoGateFiresEverything(t *testing.T) {
	r := registryWithOneHook(t)
	if !r.Has(EventPreToolUse) {
		t.Errorf("Has() with no gate should be true")
	}
	if got := r.For(EventPreToolUse, "Bash"); len(got) != 1 {
		t.Errorf("For() with no gate should return 1 entry; got %d", len(got))
	}
}

func TestRegistryUntrustedGateBlocksFor(t *testing.T) {
	r := registryWithOneHook(t)
	gate := &stubGate{} // default: not trusted
	r.SetTrustGate(gate)

	if got := r.For(EventPreToolUse, "Bash"); got != nil {
		t.Errorf("For() should return nil when untrusted; got %v", got)
	}
	if r.Has(EventPreToolUse) {
		t.Errorf("Has() should report false when untrusted")
	}
}

func TestRegistryTrustedGateAllowsLookup(t *testing.T) {
	r := registryWithOneHook(t)
	gate := &stubGate{}
	gate.trusted.Store(true)
	r.SetTrustGate(gate)

	if !r.Has(EventPreToolUse) {
		t.Errorf("Has() with trusted gate should be true")
	}
	if got := r.For(EventPreToolUse, "Bash"); len(got) != 1 {
		t.Errorf("For() with trusted gate should return 1 entry; got %d", len(got))
	}
}

// Mid-session flip: trust state changes without rebuilding the
// registry. Mirrors the user running `/trust here` while biu is
// already running — the next hook firing site picks up the change.
func TestRegistryTrustFlipTakesEffectImmediately(t *testing.T) {
	r := registryWithOneHook(t)
	gate := &stubGate{}
	r.SetTrustGate(gate)

	// Untrusted at first.
	if r.Has(EventPreToolUse) {
		t.Fatalf("setup: untrusted gate should block Has()")
	}
	// Flip to trusted — registry untouched.
	gate.trusted.Store(true)
	if !r.Has(EventPreToolUse) {
		t.Errorf("post-flip: gate should now allow lookup")
	}
	// Flip back — must block again.
	gate.trusted.Store(false)
	if r.Has(EventPreToolUse) {
		t.Errorf("re-untrust: gate should block again")
	}
}

// SkipNotifier fires when a For() lookup is blocked. Caller gets
// the event name + the underlying entry count so it can render a
// useful "X SessionStart hooks skipped" line.
func TestRegistrySkipNotifierFiresOnBlock(t *testing.T) {
	r := registryWithOneHook(t)
	gate := &stubGate{} // untrusted
	r.SetTrustGate(gate)

	var capturedEvt Event
	var capturedCount int32
	r.SetSkipNotifier(func(evt Event, count int) {
		capturedEvt = evt
		atomic.StoreInt32(&capturedCount, int32(count))
	})

	_ = r.For(EventPreToolUse, "Bash")
	if capturedEvt != EventPreToolUse {
		t.Errorf("notifier event: got %q, want PreToolUse", capturedEvt)
	}
	if got := atomic.LoadInt32(&capturedCount); got != 1 {
		t.Errorf("notifier count: got %d, want 1", got)
	}
}

// Notifier must NOT fire on success (trust passes). Keeps the
// stderr breadcrumb signal-only, not noisy.
func TestRegistrySkipNotifierSilentOnSuccess(t *testing.T) {
	r := registryWithOneHook(t)
	gate := &stubGate{}
	gate.trusted.Store(true)
	r.SetTrustGate(gate)

	var fired atomic.Bool
	r.SetSkipNotifier(func(evt Event, count int) { fired.Store(true) })

	_ = r.For(EventPreToolUse, "Bash")
	if fired.Load() {
		t.Errorf("notifier should not fire when trust passes")
	}
}

// Notifier is also silent when there were no entries to begin with —
// no point logging "0 hooks skipped". The For() early return on
// empty entries skips the gate entirely.
func TestRegistrySkipNotifierSilentWhenNoHooks(t *testing.T) {
	r := NewRegistry()
	gate := &stubGate{} // untrusted
	r.SetTrustGate(gate)

	var fired atomic.Bool
	r.SetSkipNotifier(func(evt Event, count int) { fired.Store(true) })

	_ = r.For(EventPreToolUse, "Bash")
	if fired.Load() {
		t.Errorf("notifier should not fire when no hooks are registered")
	}
}

// SetTrustGate(nil) drops the gate — useful for re-enabling hooks
// after disabling. Idempotent under repeat installs of nil.
func TestRegistrySetTrustGateNilDropsGate(t *testing.T) {
	r := registryWithOneHook(t)
	gate := &stubGate{} // untrusted
	r.SetTrustGate(gate)
	if r.Has(EventPreToolUse) {
		t.Fatal("setup: untrusted gate should block")
	}
	r.SetTrustGate(nil)
	if !r.Has(EventPreToolUse) {
		t.Errorf("nil gate should restore legacy behaviour")
	}
	r.SetTrustGate(nil) // idempotent
}

// nil-receiver safety: every public method already guards. Lock
// the contract so future refactors don't accidentally drop the
// guards.
func TestRegistryNilReceiverSafe(t *testing.T) {
	var r *Registry
	r.SetTrustGate(&stubGate{}) // must not panic
	r.SetSkipNotifier(func(Event, int) {})
	if r.Has(EventPreToolUse) {
		t.Errorf("nil registry Has must be false")
	}
	if r.For(EventPreToolUse, "x") != nil {
		t.Errorf("nil registry For must be nil")
	}
}
