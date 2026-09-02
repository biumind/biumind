package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubSemanticScanner records Run invocations so tests can assert the
// post-agent-run hook fired (and with which project / owner).
type stubSemanticScanner struct {
	calls chan [2]uuid.UUID
	err   error
}

func newStubSemanticScanner() *stubSemanticScanner {
	return &stubSemanticScanner{calls: make(chan [2]uuid.UUID, 4)}
}

func (s *stubSemanticScanner) Run(_ context.Context, projectID, ownerID uuid.UUID) error {
	s.calls <- [2]uuid.UUID{projectID, ownerID}
	return s.err
}

// triggerSemanticScan must invoke the runner asynchronously with the
// agent run's project + user ids.
func TestTriggerSemanticScan_Fires(t *testing.T) {
	s := newSrv(t)
	stub := newStubSemanticScanner()
	s.WithSemantic(stub)

	pid, uid := uuid.New(), uuid.New()
	s.triggerSemanticScan(pid, uid)

	select {
	case got := <-stub.calls:
		if got[0] != pid || got[1] != uid {
			t.Errorf("Run called with (%s, %s), want (%s, %s)", got[0], got[1], pid, uid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("semantic scanner not invoked within 2s")
	}
}

// nil Semantic (model-relay / JWT unconfigured) must be a silent no-op,
// not a panic.
func TestTriggerSemanticScan_NilScannerNoop(t *testing.T) {
	s := newSrv(t)
	s.triggerSemanticScan(uuid.New(), uuid.New()) // must not panic
}

// WithSemantic returns the server for chaining (main.go wires it in the
// relay+jwt branch alongside WithSelection).
func TestWithSemantic_Chaining(t *testing.T) {
	s := newSrv(t)
	if got := s.WithSemantic(newStubSemanticScanner()); got != s {
		t.Errorf("WithSemantic did not return the same server")
	}
	if s.Semantic == nil {
		t.Errorf("Semantic not set after WithSemantic")
	}
}
