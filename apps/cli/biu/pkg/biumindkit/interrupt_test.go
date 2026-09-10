// F5 (Phase 2) — extends followup_test.go's TestSDK_FollowupF5 with
// the post-interrupt contract:
//
//   - The terminal event is Done{StopReason:"interrupted"}, not Error.
//   - Idempotent: repeated Interrupt() is safe (no panic, returns nil).
//   - Pre-Submit Interrupt() is HELD: the next Submit starts already
//     interrupted (Done{interrupted}, provider never dialed) instead of
//     the cancel being silently dropped — the runtime worker's
//     trackAgent→Submit gap depends on this (cancel_session control
//     message can land before the first Submit exists).
//   - Parent ctx cancel WITHOUT Interrupt() still surfaces the regular
//     Error path (legacy behaviour preserved — only Interrupt() flips
//     to the clean-Done contract).
//
// followup_test.go already proves "Interrupt() makes Submit terminate";
// these tests prove the *quality* of that termination — well-formed
// state and a stop_reason embedders can render.

package biumindkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// holdingUpstream emits a single message_start frame then blocks until
// the inbound request's ctx cancels. Mirrors fakeAnthropicUpstream's
// shape in followup_test.go but exposes the request-canceled signal so
// the test can assert the upstream actually got cut off (not just the
// SDK-side ctx).
func holdingUpstream(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	canceled := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		once.Do(func() { close(canceled) })
	}))
	return srv, canceled
}

// TestSDK_InterruptEmitsCleanDone is the headline F5 contract:
// Interrupt() during a streaming turn MUST end with Done{interrupted}
// — never an Error event. Embedders rely on stop_reason to decide
// whether to render "stopped" UI vs. an error toast.
func TestSDK_InterruptEmitsCleanDone(t *testing.T) {
	upstream, upstreamCanceled := holdingUpstream(t)
	defer upstream.Close()

	a, err := New(Options{
		Model:               "test",
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := a.Submit(ctx, "long prompt")

	go func() {
		// Give the upstream time to start streaming before we cut.
		time.Sleep(100 * time.Millisecond)
		_ = a.Interrupt()
	}()

	var sawError bool
	var doneStopReason string
	for ev := range ch {
		switch e := ev.(type) {
		case Error:
			sawError = true
			t.Logf("Error event: %v", e.Err)
		case Done:
			doneStopReason = e.StopReason
		}
	}

	if sawError {
		t.Errorf("Interrupt() leaked an Error event; expected only Done{interrupted}")
	}
	if doneStopReason != "interrupted" {
		t.Errorf("Done.StopReason = %q, want \"interrupted\"", doneStopReason)
	}

	// Upstream HTTP request was actually canceled (ctx propagated end-
	// to-end, not just dropped at SDK boundary).
	select {
	case <-upstreamCanceled:
	case <-time.After(2 * time.Second):
		t.Errorf("upstream request never observed ctx cancel")
	}
}

// TestSDK_InterruptIdempotent — multiple back-to-back Interrupt() calls
// with no in-flight Submit, with one in-flight, and after channel close
// must all be safe (no panic, no error return).
func TestSDK_InterruptIdempotent(t *testing.T) {
	upstream, _ := holdingUpstream(t)
	defer upstream.Close()

	a, err := New(Options{
		Model:               "test",
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// 1. Pre-Submit Interrupt() → held for the next Submit (case 2's
	//    Submit below therefore ends Done{interrupted} — asserted in
	//    TestSDK_InterruptBeforeSubmitHoldsToNextTurn).
	if err := a.Interrupt(); err != nil {
		t.Errorf("pre-Submit Interrupt: %v", err)
	}
	if err := a.Interrupt(); err != nil {
		t.Errorf("repeat pre-Submit Interrupt: %v", err)
	}

	// 2. Submit + Interrupt + Interrupt (mid-flight)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := a.Submit(ctx, "go")

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = a.Interrupt()
		_ = a.Interrupt() // double-fire mid-flight
	}()

	for range ch {
	}

	// 3. Post-close Interrupt() → no-op
	if err := a.Interrupt(); err != nil {
		t.Errorf("post-close Interrupt: %v", err)
	}
}

// TestSDK_ParentCancelDoesNotMintInterrupted — when the user's PARENT
// ctx is canceled (timeout, app shutdown) WITHOUT going through
// Interrupt(), the engine MUST NOT mint Done{interrupted}. Only an
// explicit Agent.Interrupt() (which attaches engine.ErrInterrupted as
// the cancel cause) earns the clean-stop semantics. Plain cancels are
// either Error or silent-close — both are acceptable legacy behaviour
// and were already flaky pre-F5; the only contract we tighten here is
// "no false interrupted stop_reason."
func TestSDK_ParentCancelDoesNotMintInterrupted(t *testing.T) {
	upstream, _ := holdingUpstream(t)
	defer upstream.Close()

	a, err := New(Options{
		Model:               "test",
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch := a.Submit(ctx, "go")

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel() // PARENT cancel — bypasses Interrupt() / cause
	}()

	var doneStopReason string
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			if d, ok := ev.(Done); ok {
				doneStopReason = d.StopReason
			}
		case <-deadline.C:
			t.Fatal("Submit did not finish after parent cancel")
		}
	}

	if doneStopReason == "interrupted" {
		t.Errorf("parent cancel should NOT emit Done{interrupted}; only Interrupt() does that")
	}
}

// TestSDK_InterruptBeforeSubmitHoldsToNextTurn — Interrupt() with no
// in-flight Submit must not be dropped: the next Submit ends immediately
// with Done{interrupted} and the provider is never dialed. This is the
// runtime worker's trackAgent→Submit gap (cancel_session control message
// racing the first Submit) — dropping it meant a cancelled session ran
// the full turn anyway.
func TestSDK_InterruptBeforeSubmitHoldsToNextTurn(t *testing.T) {
	var upstreamHit atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit.Store(true)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	a, err := New(Options{
		Model:               "test",
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Cancel BEFORE any Submit exists — the trackAgent→Submit gap.
	if err := a.Interrupt(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := a.Submit(ctx, "should never run")

	var doneStopReason string
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			if d, ok := ev.(Done); ok {
				doneStopReason = d.StopReason
			}
		case <-deadline.C:
			t.Fatal("Submit hung after pre-Submit Interrupt")
		}
	}

	if doneStopReason != "interrupted" {
		t.Errorf("Done.StopReason = %q, want \"interrupted\" (cancel must not be dropped)", doneStopReason)
	}
	if upstreamHit.Load() {
		t.Error("provider was dialed despite held interrupt")
	}
}

// TestSDK_InterruptBeforeStreamStarts — Interrupt() called after Submit
// starts but BEFORE the upstream returns its first frame. Edge case:
// ctx-cancel hits the provider.Stream call itself, not the parser.
func TestSDK_InterruptBeforeStreamStarts(t *testing.T) {
	// Slow upstream: never sends headers; request hangs in connect.
	slowGate := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-slowGate:
		case <-r.Context().Done():
			return
		}
	}))
	defer upstream.Close()
	defer close(slowGate)

	a, err := New(Options{
		Model:               "test",
		APIKey:              "sk-fake",
		AnthropicEndpoint:   upstream.URL,
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
		BypassPermissions:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := a.Submit(ctx, "go")

	go func() {
		// Fire ASAP — try to land before any stream begins.
		time.Sleep(20 * time.Millisecond)
		_ = a.Interrupt()
	}()

	var doneStopReason string
	var sawError bool
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			switch e := ev.(type) {
			case Error:
				sawError = true
				t.Logf("error: %v", e.Err)
			case Done:
				doneStopReason = e.StopReason
			}
		case <-deadline.C:
			t.Fatal("Submit hung after Interrupt() pre-stream")
		}
	}

	// Either outcome is acceptable here:
	//   - Done{interrupted} if cancel hit Stream() error path
	//   - Error if the Anthropic adapter returned a wrapped non-cancel
	//     err (e.g. EOF). What we MUST NOT see is "Done{end_turn}" —
	//     that would mean we forwarded a fabricated success.
	if doneStopReason != "" && doneStopReason != "interrupted" {
		t.Errorf("unexpected Done.StopReason = %q", doneStopReason)
	}
	if !sawError && doneStopReason == "" {
		t.Errorf("Submit produced no terminal event")
	}
}
