//go:build windows

package preventsleep

import (
	"sync"
	"syscall"
)

// SetThreadExecutionState flags. We want SYSTEM_REQUIRED |
// CONTINUOUS so the assertion sticks until we explicitly clear it.
const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

type windowsImpl struct {
	mu      sync.Mutex
	running bool
	proc    *syscall.LazyProc
}

func newPlatformImpl() platformImpl {
	dll := syscall.NewLazyDLL("kernel32.dll")
	return &windowsImpl{proc: dll.NewProc("SetThreadExecutionState")}
}

func (w *windowsImpl) start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running || w.proc == nil {
		return
	}
	w.running = true
	w.callLocked(esContinuous | esSystemRequired)
}

func (w *windowsImpl) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.running || w.proc == nil {
		return
	}
	w.running = false
	w.callLocked(esContinuous)
}

func (w *windowsImpl) callLocked(flags uintptr) {
	// Single-arg syscall; ignore return + errno (best-effort).
	_, _, _ = w.proc.Call(flags)
}
