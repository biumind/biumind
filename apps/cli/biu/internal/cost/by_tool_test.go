// F4 — Tracker.AddTool / SnapshotByTool unit tests. Engine-level
// integration is tested in internal/engine/runner_cost_test.go.

package cost

import (
	"sync"
	"testing"
	"time"
)

// TestTracker_AddToolBasic — single tool, multiple calls accumulate.
func TestTracker_AddToolBasic(t *testing.T) {
	tr := NewTracker("test-model")
	tr.AddTool("Read", 100*time.Millisecond, 1024, false)
	tr.AddTool("Read", 200*time.Millisecond, 2048, false)
	tr.AddTool("Read", 50*time.Millisecond, 0, true) // error invocation

	got := tr.SnapshotByTool()
	read := got["Read"]
	if read.Calls != 3 {
		t.Errorf("Calls = %d, want 3", read.Calls)
	}
	if read.ElapsedMs != 350 {
		t.Errorf("ElapsedMs = %d, want 350", read.ElapsedMs)
	}
	if read.OutputBytes != 3072 {
		t.Errorf("OutputBytes = %d, want 3072", read.OutputBytes)
	}
	if read.Errors != 1 {
		t.Errorf("Errors = %d, want 1", read.Errors)
	}
}

// TestTracker_AddToolMultipleNames — different tools have independent
// rows.
func TestTracker_AddToolMultipleNames(t *testing.T) {
	tr := NewTracker("test")
	tr.AddTool("Read", 50*time.Millisecond, 100, false)
	tr.AddTool("Bash", 800*time.Millisecond, 4096, false)
	tr.AddTool("Bash", 200*time.Millisecond, 1024, true)

	got := tr.SnapshotByTool()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got["Read"].Calls != 1 || got["Read"].ElapsedMs != 50 {
		t.Errorf("Read: %+v", got["Read"])
	}
	if got["Bash"].Calls != 2 || got["Bash"].Errors != 1 ||
		got["Bash"].OutputBytes != 5120 {
		t.Errorf("Bash: %+v", got["Bash"])
	}
}

// TestTracker_AddToolEmptyName — defensive: empty name is dropped
// silently rather than creating a "" entry.
func TestTracker_AddToolEmptyName(t *testing.T) {
	tr := NewTracker("test")
	tr.AddTool("", 1*time.Second, 100, false)
	if got := tr.SnapshotByTool(); len(got) != 0 {
		t.Errorf("empty name should not create entry; got %v", got)
	}
}

// TestTracker_SnapshotByToolEmpty — fresh tracker returns nil (matches
// the "no data" contract; UI uses len()==0 to render "no tool calls").
func TestTracker_SnapshotByToolEmpty(t *testing.T) {
	tr := NewTracker("test")
	if got := tr.SnapshotByTool(); got != nil {
		t.Errorf("fresh tracker should return nil byTool, got %v", got)
	}
}

// TestTracker_SnapshotByToolIsCopy — mutating the snapshot must not
// affect the live tracker.
func TestTracker_SnapshotByToolIsCopy(t *testing.T) {
	tr := NewTracker("test")
	tr.AddTool("Read", 50*time.Millisecond, 100, false)

	snap := tr.SnapshotByTool()
	snap["Read"] = ToolUsage{Calls: 999} // mutate the copy

	// Live tracker still has the original value.
	live := tr.SnapshotByTool()
	if live["Read"].Calls != 1 {
		t.Errorf("snapshot mutation leaked into tracker: live=%+v", live["Read"])
	}
}

// TestTracker_AddToolConcurrent — race-detector check that AddTool is
// safe under parallel batch execution (tools.IsConcurrencySafe path).
func TestTracker_AddToolConcurrent(t *testing.T) {
	tr := NewTracker("test")
	var wg sync.WaitGroup
	const n = 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.AddTool("Read", 1*time.Millisecond, 10, false)
		}()
	}
	wg.Wait()
	got := tr.SnapshotByTool()
	if got["Read"].Calls != n {
		t.Errorf("Calls = %d, want %d", got["Read"].Calls, n)
	}
}
