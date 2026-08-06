package decay

import (
	"math"
	"testing"
	"time"
)

func TestNoDecayWhenDisabled(t *testing.T) {
	d := New(0)
	got := d.Apply(1.0, time.Now().Add(-365*24*time.Hour))
	if got != 1.0 {
		t.Errorf("got %v want 1.0", got)
	}
}

func TestHalfLife(t *testing.T) {
	now := time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC)
	d := New(30)
	d.Now = func() time.Time { return now }

	// Same instant → no decay.
	if v := d.Apply(1.0, now); math.Abs(v-1.0) > 1e-9 {
		t.Errorf("now: %v", v)
	}
	// 30 days ago → ~0.5
	thirtyDaysAgo := now.Add(-30 * 24 * time.Hour)
	if v := d.Apply(1.0, thirtyDaysAgo); math.Abs(v-0.5) > 0.001 {
		t.Errorf("30d: %v", v)
	}
	// 60 days ago → ~0.25
	sixtyDaysAgo := now.Add(-60 * 24 * time.Hour)
	if v := d.Apply(1.0, sixtyDaysAgo); math.Abs(v-0.25) > 0.001 {
		t.Errorf("60d: %v", v)
	}
}

func TestZeroTimeReturnsBase(t *testing.T) {
	d := New(30)
	if v := d.Apply(1.0, time.Time{}); v != 1.0 {
		t.Errorf("zero-time: %v", v)
	}
}
