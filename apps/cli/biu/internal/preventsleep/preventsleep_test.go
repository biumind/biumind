package preventsleep

import (
	"sync"
	"sync/atomic"
	"testing"
)

// fakeImpl counts start/stop calls so tests can assert that the OS
// hook fires only on the 0→1 / 1→0 transitions.
type fakeImpl struct {
	mu      sync.Mutex
	starts  int32
	stops   int32
	running bool
}

func (f *fakeImpl) start() {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.starts, 1)
	f.running = true
}

func (f *fakeImpl) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.stops, 1)
	f.running = false
}

func newTestState() (*State, *fakeImpl) {
	f := &fakeImpl{}
	return &State{platform: f}, f
}

func TestAcquire_startsOnce(t *testing.T) {
	s, f := newTestState()
	r1 := s.Acquire()
	r2 := s.Acquire()
	r3 := s.Acquire()
	if got := atomic.LoadInt32(&f.starts); got != 1 {
		t.Errorf("start fired %d times, want 1", got)
	}
	if s.RefCount() != 3 {
		t.Errorf("refCount = %d, want 3", s.RefCount())
	}
	r1()
	r2()
	if got := atomic.LoadInt32(&f.stops); got != 0 {
		t.Errorf("stop should not fire while refs held, got %d", got)
	}
	r3()
	if got := atomic.LoadInt32(&f.stops); got != 1 {
		t.Errorf("stop should fire on last release, got %d", got)
	}
}

func TestRelease_idempotent(t *testing.T) {
	s, f := newTestState()
	r := s.Acquire()
	r()
	r()
	r()
	if got := atomic.LoadInt32(&f.stops); got != 1 {
		t.Errorf("idempotent release: stop fired %d times, want 1", got)
	}
	if s.RefCount() != 0 {
		t.Errorf("refCount should be 0, got %d", s.RefCount())
	}
}

func TestForceStop_dropsToZero(t *testing.T) {
	s, f := newTestState()
	_ = s.Acquire()
	_ = s.Acquire()
	_ = s.Acquire()
	s.ForceStop()
	if s.RefCount() != 0 {
		t.Errorf("force stop should zero count, got %d", s.RefCount())
	}
	if got := atomic.LoadInt32(&f.stops); got != 1 {
		t.Errorf("force stop should fire stop once, got %d", got)
	}
}

func TestForceStop_noRefIsNoOp(t *testing.T) {
	s, f := newTestState()
	s.ForceStop()
	// stop() still gets called by ForceStop unconditionally — this
	// is safe (stop is idempotent at the OS layer).
	// Just verify no panic and refcount stays at zero.
	if s.RefCount() != 0 {
		t.Errorf("refCount = %d, want 0", s.RefCount())
	}
	_ = f
}

func TestActive_tracksRefCount(t *testing.T) {
	s, _ := newTestState()
	if s.Active() {
		t.Error("fresh state should not be active")
	}
	r := s.Acquire()
	if !s.Active() {
		t.Error("active after acquire")
	}
	r()
	if s.Active() {
		t.Error("not active after release")
	}
}

func TestNilSafe(t *testing.T) {
	var s *State
	r := s.Acquire()
	r() // must not panic
	s.ForceStop()
	if s.Active() {
		t.Error("nil state should never be active")
	}
	if s.RefCount() != 0 {
		t.Errorf("nil RefCount = %d, want 0", s.RefCount())
	}
}

func TestConcurrentAcquireRelease(t *testing.T) {
	s, f := newTestState()
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			r := s.Acquire()
			r()
		}()
	}
	wg.Wait()
	if s.RefCount() != 0 {
		t.Errorf("concurrent acquire/release should net to 0, got %d", s.RefCount())
	}
	// At least one start/stop pair should have happened. Exact count
	// depends on interleaving — could be 1 if all acquires landed
	// before any release, or N if perfectly serialized.
	if atomic.LoadInt32(&f.starts) == 0 || atomic.LoadInt32(&f.stops) == 0 {
		t.Errorf("expected at least one start/stop pair, got starts=%d stops=%d",
			f.starts, f.stops)
	}
	if atomic.LoadInt32(&f.starts) != atomic.LoadInt32(&f.stops) {
		t.Errorf("starts (%d) and stops (%d) should match after net-zero",
			f.starts, f.stops)
	}
}

func TestPackageDefault_acquireRelease(t *testing.T) {
	// Smoke test the package-level wrappers. Don't assert on the OS
	// implementation since this hits the real platform impl.
	r := Acquire()
	if !Active() {
		t.Error("package Acquire should make Active true")
	}
	r()
	if Active() {
		t.Error("package release should clear Active")
	}
}
