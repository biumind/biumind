//go:build !darwin && !linux && !windows

package preventsleep

// Unsupported platforms get a no-op implementation. Acquire/Release
// still increment the refcount so tests + diagnostics behave the
// same; the OS just doesn't get a sleep assertion.
type noopImpl struct{}

func newPlatformImpl() platformImpl { return noopImpl{} }

func (noopImpl) start() {}
func (noopImpl) stop()  {}
