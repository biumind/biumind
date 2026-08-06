package quota

import (
	"sync"
	"testing"
	"time"
)

// frozenClock — a controllable now() for window-rollover tests.
type frozenClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *frozenClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
func (c *frozenClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func TestCheckAndReserve_HappyPathAndExceed(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := newInMemoryLimiter(map[string]Spec{
		"hub.rpm": {Window: time.Minute, Limit: 3, Unit: "requests"},
	}, clk.now)

	for i := 0; i < 3; i++ {
		d := l.CheckAndReserve("hub.rpm", "vk-1", 1)
		if !d.Allow {
			t.Fatalf("call %d should succeed: %+v", i, d)
		}
		if d.Remaining != int64(2-i) {
			t.Errorf("call %d remaining = %d, want %d", i, d.Remaining, 2-i)
		}
	}
	d := l.CheckAndReserve("hub.rpm", "vk-1", 1)
	if d.Allow {
		t.Error("4th call should be denied")
	}
	if d.Remaining != 0 || d.Limit != 3 {
		t.Errorf("denied decision: %+v", d)
	}
	hdrs := d.Headers()
	if hdrs["X-RateLimit-Limit"] != "3" {
		t.Errorf("headers: %+v", hdrs)
	}
}

func TestCheckAndReserve_BucketWithoutSpecAlwaysAllows(t *testing.T) {
	l := NewInMemoryLimiter(nil)
	d := l.CheckAndReserve("nonexistent", "k", 999)
	if !d.Allow {
		t.Error("unspec'd bucket should allow")
	}
	if d.Limit != 0 {
		t.Error("unspec'd bucket should report no limit")
	}
}

func TestCheckAndReserve_KeysAreIndependent(t *testing.T) {
	clk := &frozenClock{t: time.Now()}
	l := newInMemoryLimiter(map[string]Spec{
		"daily": {Window: 24 * time.Hour, Limit: 1},
	}, clk.now)
	if !l.CheckAndReserve("daily", "alice", 1).Allow {
		t.Fatal("alice 1st")
	}
	if l.CheckAndReserve("daily", "alice", 1).Allow {
		t.Error("alice 2nd should deny")
	}
	if !l.CheckAndReserve("daily", "bob", 1).Allow {
		t.Error("bob shouldn't share alice's bucket")
	}
}

func TestCheckAndReserve_WindowRollover(t *testing.T) {
	clk := &frozenClock{t: time.Now()}
	l := newInMemoryLimiter(map[string]Spec{
		"hub.rpm": {Window: time.Minute, Limit: 1},
	}, clk.now)
	if !l.CheckAndReserve("hub.rpm", "vk", 1).Allow {
		t.Fatal("first call")
	}
	if l.CheckAndReserve("hub.rpm", "vk", 1).Allow {
		t.Error("within-window second should deny")
	}
	clk.advance(2 * time.Minute) // past window
	if !l.CheckAndReserve("hub.rpm", "vk", 1).Allow {
		t.Error("post-rollover should allow")
	}
}

func TestRefund(t *testing.T) {
	clk := &frozenClock{t: time.Now()}
	l := newInMemoryLimiter(map[string]Spec{
		"hub.tpm": {Window: time.Minute, Limit: 100},
	}, clk.now)
	d := l.CheckAndReserve("hub.tpm", "vk", 100)
	if !d.Allow {
		t.Fatal("reserve")
	}
	l.Refund("hub.tpm", "vk", 100)
	d = l.Snapshot("hub.tpm", "vk")
	if d.Remaining != 100 {
		t.Errorf("after refund remaining=%d, want 100", d.Remaining)
	}
}

func TestRefundClampsAtZero(t *testing.T) {
	l := NewInMemoryLimiter(map[string]Spec{
		"x": {Window: time.Minute, Limit: 10},
	})
	l.CheckAndReserve("x", "k", 5)
	l.Refund("x", "k", 999) // over-refund
	d := l.Snapshot("x", "k")
	if d.Remaining != 10 {
		t.Errorf("over-refund should clamp; got remaining=%d", d.Remaining)
	}
}

func TestSnapshotDoesNotIncrement(t *testing.T) {
	l := NewInMemoryLimiter(map[string]Spec{
		"x": {Window: time.Minute, Limit: 5},
	})
	for i := 0; i < 10; i++ {
		l.Snapshot("x", "k")
	}
	if !l.CheckAndReserve("x", "k", 5).Allow {
		t.Error("snapshot must not consume")
	}
}

func TestConcurrentReservationsHonourLimit(t *testing.T) {
	l := NewInMemoryLimiter(map[string]Spec{
		"x": {Window: time.Minute, Limit: 100},
	})
	var wg sync.WaitGroup
	var allowed, denied int64
	var mu sync.Mutex
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			if l.CheckAndReserve("x", "k", 1).Allow {
				allowed++
			} else {
				denied++
			}
		}()
	}
	wg.Wait()
	if allowed != 100 || denied != 100 {
		t.Errorf("want 100/100, got %d/%d", allowed, denied)
	}
}
