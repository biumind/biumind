package router

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// fakeClock returns a controllable now() — fixed-window math is hard
// to verify with real wall clock.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newQuotaWithClock(t *testing.T) (*ChannelQuota, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	q := NewChannelQuota()
	q.now = clk.now
	return q, clk
}

func chWithLimits(name string, rpm, tpm int) *registry.Channel {
	return &registry.Channel{
		ID:       uuid.NewSHA1(uuid.NameSpaceDNS, []byte(name)),
		RPMLimit: rpm,
		TPMLimit: tpm,
	}
}

func TestChannelQuota_NoSpecAlwaysAllows(t *testing.T) {
	q, _ := newQuotaWithClock(t)
	id := uuid.New()
	for i := 0; i < 1000; i++ {
		if err := q.AcquireRPM(id); err != nil {
			t.Fatalf("unconfigured channel should always allow: %v", err)
		}
	}
}

func TestChannelQuota_RPMHitLimit(t *testing.T) {
	q, _ := newQuotaWithClock(t)
	ch := chWithLimits("a", 3, 0) // 3 RPM, no TPM cap
	q.SetChannel(ch)

	for i := 0; i < 3; i++ {
		if err := q.AcquireRPM(ch.ID); err != nil {
			t.Fatalf("call %d under limit: %v", i+1, err)
		}
	}
	if err := q.AcquireRPM(ch.ID); !errors.Is(err, ErrChannelQuotaExhausted) {
		t.Fatalf("expected ErrChannelQuotaExhausted on 4th call, got %v", err)
	}
}

func TestChannelQuota_RPMResetsAfterWindow(t *testing.T) {
	q, clk := newQuotaWithClock(t)
	ch := chWithLimits("a", 2, 0)
	q.SetChannel(ch)

	_ = q.AcquireRPM(ch.ID)
	_ = q.AcquireRPM(ch.ID)
	if err := q.AcquireRPM(ch.ID); !errors.Is(err, ErrChannelQuotaExhausted) {
		t.Fatalf("expected exhausted")
	}
	// Advance past window
	clk.advance(61 * time.Second)
	if err := q.AcquireRPM(ch.ID); err != nil {
		t.Fatalf("post-window should reset: %v", err)
	}
}

func TestChannelQuota_TPMSoftReject(t *testing.T) {
	q, _ := newQuotaWithClock(t)
	ch := chWithLimits("a", 0, 100) // no RPM gate, 100 TPM
	q.SetChannel(ch)

	// First call sneaks through (no prior count to compare).
	if err := q.AcquireRPM(ch.ID); err != nil {
		t.Fatalf("first allowed: %v", err)
	}
	// Record tokens that push over budget.
	q.RecordTokens(ch.ID, 150)

	// Subsequent calls: TPM peek sees over-budget → reject.
	if err := q.AcquireRPM(ch.ID); !errors.Is(err, ErrChannelQuotaExhausted) {
		t.Fatalf("over-budget TPM should reject next call, got %v", err)
	}
}

func TestChannelQuota_TPMResetsAfterWindow(t *testing.T) {
	q, clk := newQuotaWithClock(t)
	ch := chWithLimits("a", 0, 100)
	q.SetChannel(ch)
	_ = q.AcquireRPM(ch.ID)
	q.RecordTokens(ch.ID, 200) // way over

	if err := q.AcquireRPM(ch.ID); !errors.Is(err, ErrChannelQuotaExhausted) {
		t.Fatalf("expected exhausted")
	}
	clk.advance(61 * time.Second)
	if err := q.AcquireRPM(ch.ID); err != nil {
		t.Fatalf("post-window TPM reset failed: %v", err)
	}
}

func TestChannelQuota_RefundRPM(t *testing.T) {
	q, _ := newQuotaWithClock(t)
	ch := chWithLimits("a", 2, 0)
	q.SetChannel(ch)
	_ = q.AcquireRPM(ch.ID)
	_ = q.AcquireRPM(ch.ID)
	if err := q.AcquireRPM(ch.ID); !errors.Is(err, ErrChannelQuotaExhausted) {
		t.Fatalf("expected exhausted")
	}
	q.RefundRPM(ch.ID) // simulate translate failure
	if err := q.AcquireRPM(ch.ID); err != nil {
		t.Fatalf("refund should free a slot: %v", err)
	}
}

func TestChannelQuota_RemoveChannel(t *testing.T) {
	q, _ := newQuotaWithClock(t)
	ch := chWithLimits("a", 1, 0)
	q.SetChannel(ch)
	_ = q.AcquireRPM(ch.ID)
	if err := q.AcquireRPM(ch.ID); !errors.Is(err, ErrChannelQuotaExhausted) {
		t.Fatalf("expected exhausted")
	}
	q.RemoveChannel(ch.ID)
	// After remove, no spec → unlimited
	for i := 0; i < 100; i++ {
		if err := q.AcquireRPM(ch.ID); err != nil {
			t.Fatalf("removed channel should be unlimited: %v", err)
		}
	}
}

func TestChannelQuota_ReloadFromListPreservesCounts(t *testing.T) {
	q, _ := newQuotaWithClock(t)
	a := chWithLimits("a", 5, 0)
	b := chWithLimits("b", 5, 0)
	q.SetChannel(a)
	q.SetChannel(b)
	_ = q.AcquireRPM(a.ID)
	_ = q.AcquireRPM(a.ID)

	// Reload with both still present + a's limit raised
	a2 := chWithLimits("a", 10, 0) // bumped
	a2.ID = a.ID
	q.ReloadFromList([]registry.Channel{*a2, *b})

	stats := q.Stats(a.ID)
	if stats.RPMLimit != 10 {
		t.Fatalf("limit not updated: %d", stats.RPMLimit)
	}
	if stats.RPMUsed != 2 {
		t.Fatalf("count NOT preserved across reload: %d", stats.RPMUsed)
	}

	// Reload removing b → b becomes unlimited
	q.ReloadFromList([]registry.Channel{*a2})
	if q.Stats(b.ID).RPMLimit != 0 {
		t.Fatalf("dropped channel should have 0 limit")
	}
}

func TestChannelQuota_IndependentBuckets(t *testing.T) {
	q, _ := newQuotaWithClock(t)
	a := chWithLimits("a", 1, 0)
	b := chWithLimits("b", 1, 0)
	q.SetChannel(a)
	q.SetChannel(b)
	_ = q.AcquireRPM(a.ID)
	if err := q.AcquireRPM(a.ID); !errors.Is(err, ErrChannelQuotaExhausted) {
		t.Fatalf("a exhausted")
	}
	if err := q.AcquireRPM(b.ID); err != nil {
		t.Fatalf("b should be independent: %v", err)
	}
}

func TestChannelQuota_Stats(t *testing.T) {
	q, clk := newQuotaWithClock(t)
	ch := chWithLimits("a", 5, 1000)
	q.SetChannel(ch)
	_ = q.AcquireRPM(ch.ID)
	q.RecordTokens(ch.ID, 250)

	s := q.Stats(ch.ID)
	if s.RPMLimit != 5 || s.RPMUsed != 1 {
		t.Errorf("rpm stats: %+v", s)
	}
	if s.TPMLimit != 1000 || s.TPMUsed != 250 {
		t.Errorf("tpm stats: %+v", s)
	}
	if s.RPMResetIn <= 0 || s.RPMResetIn > time.Minute {
		t.Errorf("rpm reset in: %v", s.RPMResetIn)
	}

	// After window: stats show 0 used
	clk.advance(61 * time.Second)
	s2 := q.Stats(ch.ID)
	if s2.RPMUsed != 0 || s2.TPMUsed != 0 {
		t.Errorf("post-window stats not reset: %+v", s2)
	}
}
