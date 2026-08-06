package compact

import (
	"errors"
	"testing"
)

type fakeSessionMem struct {
	current   string
	saved     bool
	truncated int
	saveErr   error
}

func (f *fakeSessionMem) SetCurrentState(b string) { f.current = b }
func (f *fakeSessionMem) Save() error              { f.saved = true; return f.saveErr }
func (f *fakeSessionMem) Truncate()                { f.truncated++ }

func TestPushSummaryToSessionMemory_writesAndSaves(t *testing.T) {
	w := &fakeSessionMem{}
	summary := repeat("x ", 200) // > MinSummaryBytesForSessionMemory

	if err := PushSummaryToSessionMemory(w, summary); err != nil {
		t.Fatal(err)
	}
	if w.current != summary {
		t.Errorf("current = %q, want summary", w.current[:30])
	}
	if !w.saved {
		t.Error("Save not called")
	}
	if w.truncated != 1 {
		t.Errorf("Truncate called %d times, want 1", w.truncated)
	}
}

func TestPushSummaryToSessionMemory_skipsShortSummary(t *testing.T) {
	w := &fakeSessionMem{}
	if err := PushSummaryToSessionMemory(w, "ok"); err != nil {
		t.Fatal(err)
	}
	if w.saved {
		t.Error("short summary should not save")
	}
	if w.current != "" {
		t.Error("short summary should not write")
	}
}

func TestPushSummaryToSessionMemory_nilWriterIsNoOp(t *testing.T) {
	// Must not panic.
	if err := PushSummaryToSessionMemory(nil, repeat("x ", 200)); err != nil {
		t.Errorf("nil writer should be no-op, got %v", err)
	}
}

func TestPushSummaryToSessionMemory_propagatesSaveError(t *testing.T) {
	w := &fakeSessionMem{saveErr: errors.New("disk full")}
	err := PushSummaryToSessionMemory(w, repeat("x ", 200))
	if err == nil {
		t.Error("Save error should propagate")
	}
}

func TestPushSummaryToSessionMemory_truncateBeforeSave(t *testing.T) {
	// Truncate must run BEFORE Save so the file size cap is honoured
	// on disk. The fake counts both; we assert the order via a
	// custom impl.
	type ordered struct {
		fakeSessionMem
		truncatedAt, savedAt int
		seq                  int
	}
	w := &ordered{}
	w.fakeSessionMem.saveErr = nil
	// Override Save / Truncate to record order.
	saveStub := func() error { w.seq++; w.savedAt = w.seq; return nil }
	truncStub := func() { w.seq++; w.truncatedAt = w.seq }
	wrapper := &orderedAdapter{
		setCurrent: func(b string) { w.current = b },
		save:       saveStub,
		truncate:   truncStub,
	}

	if err := PushSummaryToSessionMemory(wrapper, repeat("x ", 200)); err != nil {
		t.Fatal(err)
	}
	if w.truncatedAt == 0 || w.savedAt == 0 {
		t.Fatalf("truncate=%d save=%d — both should fire", w.truncatedAt, w.savedAt)
	}
	if w.truncatedAt > w.savedAt {
		t.Errorf("truncate must run before save; truncatedAt=%d savedAt=%d",
			w.truncatedAt, w.savedAt)
	}
}

// orderedAdapter is a SessionMemoryWriter built from injected fns
// for the order-of-operations test.
type orderedAdapter struct {
	setCurrent func(string)
	save       func() error
	truncate   func()
}

func (o *orderedAdapter) SetCurrentState(b string) { o.setCurrent(b) }
func (o *orderedAdapter) Save() error              { return o.save() }
func (o *orderedAdapter) Truncate()                { o.truncate() }

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
