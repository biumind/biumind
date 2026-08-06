package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// TestResolveResumeID_Precedence — explicit --resume wins over
// --continue, and --resume bypasses the FindLatest lookup entirely
// (so a stale-or-misconfigured sessions dir doesn't block an
// explicit id).
func TestResolveResumeID_Precedence(t *testing.T) {
	called := false
	find := func(_ string) (session.Summary, bool) {
		called = true
		return session.Summary{ID: "auto"}, true
	}
	got, ok, err := resolveResumeID("/dir", "explicit-id", true, find)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok || got != "explicit-id" {
		t.Errorf("got (%q,%v), want (explicit-id, true)", got, ok)
	}
	if called {
		t.Errorf("FindLatest must not run when --resume is explicit")
	}
}

// TestResolveResumeID_ContinuePicksLatest — --continue without
// --resume uses the find() callback to pick the most recent session.
func TestResolveResumeID_ContinuePicksLatest(t *testing.T) {
	find := func(_ string) (session.Summary, bool) {
		return session.Summary{ID: "latest-id"}, true
	}
	got, ok, err := resolveResumeID("/dir", "", true, find)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok || got != "latest-id" {
		t.Errorf("got (%q,%v), want (latest-id, true)", got, ok)
	}
}

// TestResolveResumeID_ContinueWithNoSessions — --continue with an
// empty sessions dir returns a hinted error so the user knows what
// went wrong.
func TestResolveResumeID_ContinueWithNoSessions(t *testing.T) {
	find := func(_ string) (session.Summary, bool) {
		return session.Summary{}, false
	}
	_, _, err := resolveResumeID("/dir", "", true, find)
	if err == nil {
		t.Fatal("expected error when --continue has nothing to pick")
	}
	if !strings.Contains(err.Error(), "--continue") {
		t.Errorf("error should mention --continue: %v", err)
	}
	// Hint surface: error text should suggest a remedy.
	if !strings.Contains(err.Error(), "biu without --continue") {
		t.Errorf("expected hint on remedy: %v", err)
	}
}

// TestResolveResumeID_NeitherFlag — both flags off ⇒ no resume,
// no error, no FindLatest call (pure short-circuit).
func TestResolveResumeID_NeitherFlag(t *testing.T) {
	called := false
	find := func(_ string) (session.Summary, bool) {
		called = true
		return session.Summary{}, true
	}
	got, ok, err := resolveResumeID("/dir", "", false, find)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok || got != "" {
		t.Errorf("expected no-op; got (%q,%v)", got, ok)
	}
	if called {
		t.Errorf("find must not be called when neither flag is set")
	}
}

// TestResolveResumeID_FindError — verifies the find callback's miss
// path doesn't conflate with the not-supplied case (tests that
// ok=false from find produces an error, distinct from neither flag).
func TestResolveResumeID_FindError(t *testing.T) {
	// Sentinel for parity with future test additions.
	sentinel := errors.New("never thrown")
	_ = sentinel
	got, ok, err := resolveResumeID("/dir", "", true, func(_ string) (session.Summary, bool) {
		return session.Summary{}, false
	})
	if err == nil {
		t.Fatal("expected error on no-sessions")
	}
	if got != "" || ok {
		t.Errorf("error case must return zero/false; got (%q,%v)", got, ok)
	}
}
