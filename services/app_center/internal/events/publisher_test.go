package events

import (
	"testing"
)

// SDKBridge with nil publisher must NOT error — events are non-critical
// telemetry; a stateless service should silently succeed rather than
// break the calling App.
func TestSDKBridge_NilPubReturnsNil(t *testing.T) {
	b := &SDKBridge{Pub: nil}
	if err := b.PublishViewDataChanged(t.Context(), "install-1"); err != nil {
		t.Errorf("nil pub should silently succeed, got %v", err)
	}
}

// IdentifierFor=nil is supported (events table accepts empty actor_id
// rows; ensures bridge doesn't NPE on first call before resolver wiring).
func TestSDKBridge_NilResolver(t *testing.T) {
	b := &SDKBridge{
		Pub: &PgxPublisher{Pool: nil},
		IdentifierFor: nil,
	}
	// With pool=nil the underlying call errors; we just want to confirm
	// the resolver guard runs first.
	err := b.PublishViewDataChanged(t.Context(), "install-1", "view-a")
	if err == nil {
		t.Fatal("expected pool-nil error")
	}
}
