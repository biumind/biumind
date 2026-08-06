// Package preventsleep keeps the host machine awake while biu is doing
// long-running work (API calls, tool execution, agent loops). It covers
// macOS, Linux, and Windows.
//
// Usage is reference-counted: call Acquire when starting a guarded
// operation and invoke the returned release func when finished. The
// underlying OS assertion is created on the first acquire and torn
// down once the count returns to zero.
//
// Design notes:
//
//   - Self-healing: the platform implementation re-arms the assertion
//     on a 4-minute interval so a hard-kill of biu (SIGKILL, panic) is
//     bounded by the OS-level 5-minute timeout instead of pinning the
//     machine awake forever.
//   - Silent-fail: if the OS tool is missing (no caffeinate, no
//     systemd-inhibit) we just no-op. Sleep prevention is best-effort,
//     never load-bearing.
//   - Nil-safe: every public entry point tolerates nil so callers can
//     guard a code path without branching.
package preventsleep

import "sync"

// State holds the refcount and platform-specific handle. Most users
// should reach for the package-level Acquire/ForceStop helpers, which
// drive a singleton instance.
type State struct {
	mu       sync.Mutex
	refCount int
	platform platformImpl
}

// platformImpl is implemented per-OS in preventsleep_<goos>.go.
type platformImpl interface {
	start() // start or restart the OS assertion (idempotent).
	stop()  // tear down the OS assertion (idempotent).
}

var defaultState = &State{platform: newPlatformImpl()}

// Acquire bumps the refcount and ensures the OS assertion is active.
// The returned func releases the hold; calling it more than once is a
// no-op so deferred-release patterns are safe.
func Acquire() func() {
	return defaultState.Acquire()
}

// ForceStop drops the refcount to zero and tears down the OS
// assertion regardless of outstanding holds. Intended for shutdown
// paths where we'd rather under-protect than leave caffeinate
// running.
func ForceStop() { defaultState.ForceStop() }

// Active reports whether the assertion is currently held. Useful in
// tests + status reporting; not part of the hot path.
func Active() bool { return defaultState.Active() }

// Acquire on a State returns a release func. Safe on a nil receiver
// (returns a no-op release).
func (s *State) Acquire() func() {
	if s == nil {
		return func() {}
	}
	s.mu.Lock()
	s.refCount++
	first := s.refCount == 1
	s.mu.Unlock()
	if first && s.platform != nil {
		s.platform.start()
	}
	var once sync.Once
	return func() { once.Do(func() { s.release() }) }
}

func (s *State) release() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.refCount > 0 {
		s.refCount--
	}
	last := s.refCount == 0
	s.mu.Unlock()
	if last && s.platform != nil {
		s.platform.stop()
	}
}

// ForceStop drops to zero unconditionally. Safe on nil.
func (s *State) ForceStop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.refCount = 0
	s.mu.Unlock()
	if s.platform != nil {
		s.platform.stop()
	}
}

// Active reports whether refCount > 0. Safe on nil.
func (s *State) Active() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refCount > 0
}

// RefCount exposes the current count for tests + diagnostics. Safe on
// nil.
func (s *State) RefCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refCount
}
