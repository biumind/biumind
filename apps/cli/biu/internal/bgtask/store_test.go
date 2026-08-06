package bgtask

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// helper: spawn + wait synchronously up to a timeout. Tests can't
// reliably block on Done() forever in case of regressions.
func spawnAndWait(t *testing.T, s *Store, cmd string) Snapshot {
	t.Helper()
	task, err := s.Spawn(context.Background(), SpawnRequest{Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-task.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("task %s never completed", task.ID)
	}
	return task.Snapshot()
}

func TestSpawnRequiresCommand(t *testing.T) {
	s := NewStore()
	if _, err := s.Spawn(context.Background(), SpawnRequest{}); err == nil {
		t.Errorf("empty command should fail")
	}
	if _, err := s.Spawn(context.Background(), SpawnRequest{Command: "  "}); err == nil {
		t.Errorf("whitespace-only command should fail")
	}
}

func TestSpawnReturnsImmediatelyAndIDsAreUnique(t *testing.T) {
	s := NewStore()
	// Use sleep 1 — long enough that Spawn must NOT block on it.
	start := time.Now()
	t1, err := s.Spawn(context.Background(), SpawnRequest{Command: "sleep 0.5"})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.Spawn(context.Background(), SpawnRequest{Command: "sleep 0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("Spawn appears to block on the command; took %s", took)
	}
	if t1.ID == t2.ID {
		t.Errorf("IDs collided: %s == %s", t1.ID, t2.ID)
	}
	// Drain so we don't leave processes for the rest of the test suite.
	<-t1.Done()
	<-t2.Done()
}

func TestSpawnSuccessRecordsExitZero(t *testing.T) {
	s := NewStore()
	snap := spawnAndWait(t, s, `printf "hello\n"; printf "world\n"`)
	if snap.Status != StatusDone {
		t.Errorf("status: got %q, want done", snap.Status)
	}
	if snap.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", snap.ExitCode)
	}
	if snap.TotalLines != 2 {
		t.Errorf("total lines: got %d, want 2", snap.TotalLines)
	}
}

func TestSpawnFailureRecordsExitCode(t *testing.T) {
	s := NewStore()
	snap := spawnAndWait(t, s, `echo failing; exit 7`)
	if snap.Status != StatusFailed {
		t.Errorf("status: got %q, want failed", snap.Status)
	}
	if snap.ExitCode != 7 {
		t.Errorf("exit code: got %d, want 7", snap.ExitCode)
	}
}

func TestOutputDeltaPolling(t *testing.T) {
	s := NewStore()
	task, err := s.Spawn(context.Background(), SpawnRequest{
		// Three lines, one per second-fragment so we can poll between.
		Command: `printf "first\n"; sleep 0.1; printf "second\n"; sleep 0.1; printf "third\n"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-task.Done()

	// Poll all at once — sinceLine=0 returns everything.
	lines, next, status, dropped, ok := s.Output(task.ID, 0)
	if !ok {
		t.Fatal("Output should find the task")
	}
	if status != StatusDone {
		t.Errorf("status: %q", status)
	}
	if dropped != 0 {
		t.Errorf("dropped should be 0; got %d", dropped)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines; got %v", lines)
	}
	if next != 3 {
		t.Errorf("next: got %d, want 3", next)
	}

	// Re-poll from `next` — should be empty.
	rest, next2, _, _, _ := s.Output(task.ID, next)
	if len(rest) != 0 {
		t.Errorf("re-poll should be empty; got %v", rest)
	}
	if next2 != next {
		t.Errorf("re-poll next should equal previous; got %d", next2)
	}
}

func TestOutputUnknownIDReturnsNotOk(t *testing.T) {
	s := NewStore()
	_, _, _, _, ok := s.Output("bg-9999", 0)
	if ok {
		t.Errorf("missing id should yield ok=false")
	}
}

func TestStopRunningTaskRecordsKilled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal semantics tested on POSIX only")
	}
	s := NewStore()
	task, err := s.Spawn(context.Background(), SpawnRequest{
		Command: `sleep 30`,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Give the process a moment to register before we kill it.
	time.Sleep(50 * time.Millisecond)

	snap, err := s.Stop(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != StatusKilled {
		t.Errorf("status after stop: %q, want killed", snap.Status)
	}
	if snap.EndedAt.IsZero() {
		t.Errorf("endedAt should be set after Stop")
	}
}

func TestStopAlreadyTerminatedIsNoop(t *testing.T) {
	s := NewStore()
	snap := spawnAndWait(t, s, `printf done`)
	if snap.Status != StatusDone {
		t.Fatalf("setup: want done; got %q", snap.Status)
	}
	// Now Stop the already-finished task — should report current
	// state, not flip to "killed".
	got, err := s.Stop(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone {
		t.Errorf("Stop on done task should preserve status; got %q", got.Status)
	}
}

func TestStopUnknownIDFails(t *testing.T) {
	s := NewStore()
	if _, err := s.Stop("bg-9999"); err == nil {
		t.Errorf("missing id should fail")
	}
}

func TestKillsEntireProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups tested on POSIX only")
	}
	s := NewStore()
	// Pipeline — when Stop fires, BOTH `sleep` and `tail` should die,
	// not just the leading sh. Without process-group semantics the
	// inner `sleep` would orphan and pin the test for 60s.
	task, err := s.Spawn(context.Background(), SpawnRequest{
		Command: `sleep 60 | sleep 60 | sleep 60`,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	stopStart := time.Now()
	if _, err := s.Stop(task.ID); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(stopStart); took > 3*time.Second {
		t.Errorf("Stop didn't kill the pipeline cleanly; took %s", took)
	}
}

func TestListSortsByNumericID(t *testing.T) {
	s := NewStore()
	for i := 0; i < 12; i++ {
		_, _ = s.Spawn(context.Background(), SpawnRequest{Command: "true"})
	}
	// Wait for all to finish so the snapshots are deterministic.
	time.Sleep(200 * time.Millisecond)

	rows := s.List()
	if len(rows) != 12 {
		t.Fatalf("expected 12 tasks; got %d", len(rows))
	}
	// Lexical sort would put bg-10 right after bg-1; numeric must
	// keep them in spawn order.
	wantOrder := []string{
		"bg-1", "bg-2", "bg-3", "bg-4", "bg-5", "bg-6",
		"bg-7", "bg-8", "bg-9", "bg-10", "bg-11", "bg-12",
	}
	for i, w := range wantOrder {
		if rows[i].ID != w {
			t.Errorf("row %d: got %q, want %q", i, rows[i].ID, w)
		}
	}
}

func TestStopAllTerminatesRunningTasks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only")
	}
	s := NewStore()
	// Spawn 3 long-runners + 1 instant.
	for i := 0; i < 3; i++ {
		_, _ = s.Spawn(context.Background(), SpawnRequest{Command: "sleep 30"})
	}
	doneTask, _ := s.Spawn(context.Background(), SpawnRequest{Command: "true"})
	<-doneTask.Done()
	// Give the long-runners a moment to register.
	time.Sleep(50 * time.Millisecond)

	killed := s.StopAll()
	if killed != 3 {
		t.Errorf("StopAll killed %d tasks, want 3", killed)
	}
	for _, snap := range s.List() {
		if snap.Status == StatusRunning {
			t.Errorf("task %s still running after StopAll", snap.ID)
		}
	}
}

func TestBufferCapDropsOldestLines(t *testing.T) {
	// Override the cap is impossible without exporting it; instead
	// verify the buffer growth + dropped counter at the documented
	// MaxBufferLines threshold by force-inserting via appendLine.
	t.Run("appendLine drops oldest at cap", func(t *testing.T) {
		task := &Task{}
		// One past the cap so we trigger the drop path.
		for i := 0; i < MaxBufferLines+50; i++ {
			task.appendLine("x")
		}
		if got := len(task.lines); got != MaxBufferLines {
			t.Errorf("buffer size: got %d, want %d", got, MaxBufferLines)
		}
		if task.dropped != 50 {
			t.Errorf("dropped: got %d, want 50", task.dropped)
		}
	})
}

// Concurrent Spawn + List must not race — the data-race detector
// catches accidental shared-map mutation.
func TestConcurrentSpawnAndListIsRaceFree(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Spawn(context.Background(), SpawnRequest{Command: "true"})
		}()
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.List()
		}()
	}
	wg.Wait()
	// Sanity: at least one task survived.
	if len(s.List()) == 0 {
		t.Errorf("no tasks recorded under concurrency")
	}
}

func TestSnapshotSeesStreamedOutput(t *testing.T) {
	s := NewStore()
	task, err := s.Spawn(context.Background(), SpawnRequest{
		Command: `for i in 1 2 3; do printf "line $i\n"; done`,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-task.Done()

	lines, _, _, _, _ := s.Output(task.ID, 0)
	got := strings.Join(lines, "|")
	if got != "line 1|line 2|line 3" {
		t.Errorf("output ordering wrong: %q", got)
	}
}

// Pending returns + drains in one shot — the engine's contract is
// "tell me what finished since I last asked, then forget". Two
// rapid drains must not double-report the same task.
func TestPendingDrainsOnceAndClears(t *testing.T) {
	s := NewStore()
	for i := 0; i < 3; i++ {
		_, _ = s.Spawn(context.Background(), SpawnRequest{Command: "true"})
	}
	// Wait for all reapers to settle.
	time.Sleep(150 * time.Millisecond)

	got := s.Pending()
	if len(got) != 3 {
		t.Fatalf("first drain should report 3 completions; got %d", len(got))
	}
	if got2 := s.Pending(); got2 != nil {
		t.Errorf("second drain should be empty; got %v", got2)
	}
}

// New completions arriving AFTER a drain must show up in the next
// drain — not be coalesced into the previous one.
func TestPendingAccumulatesBetweenDrains(t *testing.T) {
	s := NewStore()
	_, _ = s.Spawn(context.Background(), SpawnRequest{Command: "true"})
	time.Sleep(50 * time.Millisecond)
	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("first batch: got %d, want 1", len(got))
	}
	// Spawn two more; their reapers should re-arm the queue.
	for i := 0; i < 2; i++ {
		_, _ = s.Spawn(context.Background(), SpawnRequest{Command: "true"})
	}
	time.Sleep(100 * time.Millisecond)
	if got := s.Pending(); len(got) != 2 {
		t.Errorf("second batch: got %d, want 2", len(got))
	}
}

// markCompleted is idempotent under double-fire (reaper + Stop both
// trying to record the same terminal state).
func TestMarkCompletedDeduplicates(t *testing.T) {
	s := &Store{tasks: map[string]*Task{
		"bg-1": {ID: "bg-1", status: StatusDone, doneCh: make(chan struct{})},
	}}
	s.markCompleted("bg-1")
	s.markCompleted("bg-1") // duplicate — same task hit by both pathways
	if len(s.pending) != 1 {
		t.Errorf("dedup failed; pending=%v", s.pending)
	}
}

// Tail returns the last N lines, capped by what's actually buffered.
// Used by the notification builder so the model sees a preview
// without polling BashOutput.
func TestTailReturnsLastNLines(t *testing.T) {
	s := NewStore()
	task, _ := s.Spawn(context.Background(), SpawnRequest{
		Command: `printf "1\n2\n3\n4\n5\n"`,
	})
	<-task.Done()

	got := s.Tail(task.ID, 3)
	if len(got) != 3 || got[0] != "3" || got[2] != "5" {
		t.Errorf("Tail(3) returned %v; want [3 4 5]", got)
	}
	// More than buffered — return whatever's available.
	got = s.Tail(task.ID, 100)
	if len(got) != 5 {
		t.Errorf("Tail(100) on 5-line task returned %d lines; want 5", len(got))
	}
	if s.Tail(task.ID, 0) != nil {
		t.Errorf("Tail(0) should return nil")
	}
	if s.Tail("bg-9999", 5) != nil {
		t.Errorf("Tail on unknown id should return nil")
	}
}

func TestStderrTagged(t *testing.T) {
	s := NewStore()
	snap := spawnAndWait(t, s, `printf "good\n"; printf "bad\n" >&2`)
	if snap.Status != StatusDone {
		t.Errorf("expected clean exit; got %q", snap.Status)
	}
	lines, _, _, _, _ := s.Output(snap.ID, 0)
	tagged := false
	for _, l := range lines {
		if strings.HasPrefix(l, "[stderr] bad") {
			tagged = true
		}
	}
	if !tagged {
		t.Errorf("stderr line should be tagged: %v", lines)
	}
}
