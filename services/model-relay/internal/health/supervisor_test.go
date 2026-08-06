package health

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// End-to-end auto-disable + recovery cycle:
//  1. Request path RecordFailure × 5 → channel flips to auto_disabled
//  2. Sweep tick (forced) probes via stub upstream → healthy
//  3. Channel recovers to active, failure_count reset
func TestSupervisorAutoDisableThenRecover(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	ctx := context.Background()

	sup := NewSupervisor(fx.probe, fx.store, SupervisorConfig{
		FailThreshold: 5,
		// Cooldown 0 so the sweep picks up the just-disabled row in
		// the same tick — keeps the test fast.
		Cooldown:      1 * time.Microsecond, // tiny — sentinel "no cooldown"
		SweepInterval: 50 * time.Millisecond,
		PerSweepLimit: 10,
	})
	sup.Start(ctx)
	defer sup.Close()

	// 5 failures via the request-path API
	for i := 0; i < 5; i++ {
		_, status, err := sup.RecordFailure(ctx, fx.channel.ID, errors.New("boom"))
		if err != nil {
			t.Fatalf("record_failure %d: %v", i, err)
		}
		if i == 4 && status != registry.StatusAutoDisabled {
			t.Fatalf("expected auto_disabled at 5th failure, got %s", status)
		}
	}

	// Wait for sweep to run — it polls every 50ms; 1s is plenty.
	deadline := time.Now().Add(2 * time.Second)
	var got *registry.Channel
	for time.Now().Before(deadline) {
		var err error
		got, err = fx.store.Channels.Get(ctx, fx.channel.ID)
		if err == nil && got.Status == registry.StatusActive {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if got == nil || got.Status != registry.StatusActive {
		t.Fatalf("channel did not recover within deadline: %+v", got)
	}
	if got.FailureCount != 0 {
		t.Fatalf("failure count not reset: %d", got.FailureCount)
	}
	if got.LatencyP50Ms == 0 {
		t.Fatalf("latency not recorded during recovery")
	}
}

// Sweep should NOT recover a channel whose probe still fails.
func TestSupervisorSweepLeavesUnhealthyAlone(t *testing.T) {
	fx := newProbeFixture(t, "500")
	ctx := context.Background()

	sup := NewSupervisor(fx.probe, fx.store, SupervisorConfig{
		FailThreshold: 5,
		Cooldown:      1 * time.Microsecond, // tiny — sentinel "no cooldown"
		SweepInterval: 50 * time.Millisecond,
		PerSweepLimit: 10,
	})

	// Pre-set channel to auto_disabled
	_, _ = fx.pool.Exec(ctx,
		`UPDATE model_relay.channels
		   SET status='auto_disabled', failure_count=5,
		       last_error='preset', last_error_at=now() - interval '1 hour'
		 WHERE id=$1`, fx.channel.ID)

	sup.Start(ctx)
	defer sup.Close()

	// Allow a couple of sweeps
	time.Sleep(300 * time.Millisecond)

	got, err := fx.store.Channels.Get(ctx, fx.channel.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != registry.StatusAutoDisabled {
		t.Fatalf("upstream still failing — channel should remain auto_disabled, got %s",
			got.Status)
	}
	// Probe ran (last_test_at advanced); last_error should be a 500-ish msg
	if got.LastTestAt == nil {
		t.Fatalf("expected probe to stamp last_test_at")
	}
}

// Cooldown is respected: a freshly-disabled channel within cooldown
// is NOT picked up.
func TestSupervisorCooldownGate(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	ctx := context.Background()

	sup := NewSupervisor(fx.probe, fx.store, SupervisorConfig{
		FailThreshold: 5,
		Cooldown:      10 * time.Minute, // long
		SweepInterval: 50 * time.Millisecond,
		PerSweepLimit: 10,
	})

	// Mark just-disabled (last_error_at = now)
	_, _ = fx.pool.Exec(ctx,
		`UPDATE model_relay.channels
		   SET status='auto_disabled', failure_count=5,
		       last_error_at=now()
		 WHERE id=$1`, fx.channel.ID)

	sup.Start(ctx)
	defer sup.Close()

	time.Sleep(200 * time.Millisecond)

	got, err := fx.store.Channels.Get(ctx, fx.channel.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != registry.StatusAutoDisabled {
		t.Fatalf("cooldown not respected: channel transitioned to %s within window",
			got.Status)
	}
}

func TestSupervisorRecordSuccess(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	ctx := context.Background()

	sup := NewSupervisor(fx.probe, fx.store, SupervisorConfig{})

	// Push channel into auto_disabled via 5 failures
	for i := 0; i < 5; i++ {
		_, _, _ = sup.RecordFailure(ctx, fx.channel.ID, errors.New("e"))
	}
	pre, _ := fx.store.Channels.Get(ctx, fx.channel.ID)
	if pre.Status != registry.StatusAutoDisabled {
		t.Fatalf("setup: expected auto_disabled, got %s", pre.Status)
	}

	if err := sup.RecordSuccess(ctx, fx.channel.ID, 123); err != nil {
		t.Fatalf("record_success: %v", err)
	}
	got, _ := fx.store.Channels.Get(ctx, fx.channel.ID)
	if got.Status != registry.StatusActive {
		t.Fatalf("expected active after success, got %s", got.Status)
	}
	if got.LatencyP50Ms != 123 {
		t.Fatalf("expected latency=123, got %d", got.LatencyP50Ms)
	}
}

func TestSupervisorPanicsOnNilDeps(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	_ = NewSupervisor(nil, nil, SupervisorConfig{})
}

// Ensure Close is safe to call repeatedly.
func TestSupervisorCloseIdempotent(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	sup := NewSupervisor(fx.probe, fx.store, SupervisorConfig{
		SweepInterval: 1 * time.Hour, // effectively never
	})
	sup.Start(context.Background())
	sup.Close()
	sup.Close()
}

// Smoke: stamp probe-failure path doesn't blow up. Sweep on a channel
// whose probe returns 401 should NOT call RecordSuccess.
func TestSupervisorProbeFailureStampsTimestamp(t *testing.T) {
	fx := newProbeFixture(t, "401")
	ctx := context.Background()

	sup := NewSupervisor(fx.probe, fx.store, SupervisorConfig{
		FailThreshold: 5,
		Cooldown:      1 * time.Microsecond, // tiny — sentinel "no cooldown"
		SweepInterval: 50 * time.Millisecond,
		PerSweepLimit: 10,
	})

	_, _ = fx.pool.Exec(ctx,
		`UPDATE model_relay.channels
		   SET status='auto_disabled', failure_count=5,
		       last_error='', last_error_at=now() - interval '1 hour'
		 WHERE id=$1`, fx.channel.ID)
	sup.Start(ctx)
	defer sup.Close()

	time.Sleep(300 * time.Millisecond)

	got, _ := fx.store.Channels.Get(ctx, fx.channel.ID)
	if got.Status != registry.StatusAutoDisabled {
		t.Fatalf("should remain disabled")
	}
	if got.LastError == "" {
		t.Fatalf("expected last_error to be stamped after probe failure")
	}
	// Verify cooldown: last_error_at should be recent (within last few sec)
	if got.LastErrorAt == nil || time.Since(*got.LastErrorAt) > 5*time.Second {
		t.Fatalf("last_error_at should be recent: %v", got.LastErrorAt)
	}
}

// Make sure log compiles + records (fmt only — ensures no unused var)
func init() {
	_ = fmt.Sprintf("compile-check")
}
