// Package bgtask owns the registry of background shell tasks that
// `Bash{run_in_background: true}` spawns. The model uses BashOutput
// to poll captured stdout/stderr and KillBash to terminate.
//
// Contract:
//
//   * Spawn returns immediately with a task ID; the caller doesn't
//     block on completion.
//   * Output streams into a per-task buffer (line-by-line), readable
//     from any goroutine via Output().
//   * Stop sends SIGTERM (then SIGKILL after a short grace) so a
//     well-behaved process gets a chance to flush.
//   * Tasks are tracked by string ID (`bg-1`, `bg-2`, …) for the
//     lifetime of the Store; the REPL surfaces them via /tasks.
//
// Concurrency: every public method is safe for concurrent calls.
// The Store's internal map is mutex-guarded; per-task buffers use
// their own mutex so a Stop on one task doesn't stall a concurrent
// Output read on another.

package bgtask

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Status enumerates the lifecycle states a background task moves
// through. Mirrors the strings the REPL prints so the values are
// stable across versions.
type Status string

const (
	StatusRunning  Status = "running"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
	StatusKilled   Status = "killed"
)

// MaxBufferLines caps the per-task output ring. 10 000 lines is
// enough for tail-style monitoring without unbounded memory growth.
// When exceeded, the oldest lines drop and Output() reports a
// "truncated" notice.
const MaxBufferLines = 10_000

// gracePeriod is the SIGTERM → SIGKILL escalation window. Short
// enough that hung commands die quickly; long enough that
// well-behaved processes flush stdio.
const gracePeriod = 2 * time.Second

// Task is one running / completed background shell task.
type Task struct {
	ID        string
	Command   string
	Cwd       string
	StartedAt time.Time

	// Mutable fields below are guarded by mu.
	mu          sync.Mutex
	status      Status
	endedAt     time.Time
	exitCode    int
	lines       []string
	dropped     int // number of lines lost to buffer cap
	cancel      context.CancelFunc
	cmd         *exec.Cmd
	doneCh      chan struct{}
}

// Snapshot is a stable read-only view of a Task — what /tasks
// list rows render and what BashOutput reports back.
type Snapshot struct {
	ID        string
	Command   string
	Cwd       string
	Status    Status
	StartedAt time.Time
	EndedAt   time.Time // zero when still running
	ExitCode  int
	TotalLines int
	Dropped    int
}

// Snapshot returns the current task state without exposing the
// underlying mutex / cmd handle.
func (t *Task) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Snapshot{
		ID: t.ID, Command: t.Command, Cwd: t.Cwd,
		Status:     t.status,
		StartedAt:  t.StartedAt,
		EndedAt:    t.endedAt,
		ExitCode:   t.exitCode,
		TotalLines: len(t.lines),
		Dropped:    t.dropped,
	}
}

// Done is closed when the task transitions to a terminal status.
// Callers wanting notification can `select` on this without holding
// the task lock.
func (t *Task) Done() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.doneCh
}

// appendLine adds a line to the task's buffer with cap enforcement.
// The buffer behaves as a FIFO ring — the oldest line is dropped
// once the cap is reached so we never grow unboundedly under a
// chatty process.
func (t *Task) appendLine(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) >= MaxBufferLines {
		// Drop oldest. Slice re-allocation is amortized O(1) thanks to
		// the slice's exponential growth, and we're capped at 10K so
		// the memory footprint stays bounded.
		t.lines = t.lines[1:]
		t.dropped++
	}
	t.lines = append(t.lines, s)
}

// Store is the package's exported surface — one instance per biu
// process, shared by Bash, BashOutput, KillBash, and the REPL.
type Store struct {
	mu      sync.Mutex
	counter int64
	tasks   map[string]*Task

	// pending holds task IDs that transitioned to a terminal state
	// since the last Pending() drain. The engine consults this at
	// the head of every user turn to fold a "task X finished" system
	// attachment into the conversation — the model doesn't have to
	// poll BashOutput on a timer.
	pending []string
}

// NewStore returns an empty store. Cheap; no goroutines started.
func NewStore() *Store {
	return &Store{tasks: map[string]*Task{}}
}

// SpawnRequest configures a new background task.
type SpawnRequest struct {
	// Command is the shell command run via /bin/sh -c. Required.
	Command string
	// Cwd is the working directory. Empty = inherit from caller.
	Cwd string
	// Env, if non-nil, replaces the inherited environment.
	Env []string
}

// Spawn forks the command and registers a Task. Returns the new
// Task immediately; the caller does NOT block on completion.
//
// The supplied parent ctx controls the task's lifetime: cancelling
// it kills the process. To detach the task from the parent's
// cancellation (e.g. so an Agent dispatch ending doesn't kill the
// background work), pass context.Background().
func (s *Store) Spawn(parent context.Context, req SpawnRequest) (*Task, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("bgtask: command is required")
	}

	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", req.Command)
	cmd.Dir = req.Cwd
	if req.Env != nil {
		cmd.Env = req.Env
	}
	// Put the child in its own process group so a KillBash can take
	// out the entire pipeline (`tail -f log | grep ERROR`), not just
	// the leading sh.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bgtask: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("bgtask: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("bgtask: start: %w", err)
	}

	id := s.nextID()
	t := &Task{
		ID:        id,
		Command:   req.Command,
		Cwd:       req.Cwd,
		StartedAt: time.Now(),
		status:    StatusRunning,
		cancel:    cancel,
		cmd:       cmd,
		doneCh:    make(chan struct{}),
	}

	s.mu.Lock()
	s.tasks[id] = t
	s.mu.Unlock()

	// Drain stdout + stderr concurrently so a chatty stream doesn't
	// block the other.
	var wg sync.WaitGroup
	wg.Add(2)
	go drain(&wg, t, stdoutPipe, "stdout")
	go drain(&wg, t, stderrPipe, "stderr")

	// Reaper: wait for the process, mark terminal status, close
	// doneCh. Runs in its own goroutine so Spawn returns instantly.
	go func() {
		wg.Wait() // streams must finish before we record exit code
		err := cmd.Wait()
		t.mu.Lock()
		t.endedAt = time.Now()
		switch {
		case t.status == StatusKilled:
			// Stop already recorded the terminal state; preserve it.
		case err == nil:
			t.status = StatusDone
			t.exitCode = 0
		default:
			t.exitCode = -1
			if ee, ok := err.(*exec.ExitError); ok {
				t.exitCode = ee.ExitCode()
			}
			// Use ctx.Err to distinguish "killed by Stop" from
			// "process exited non-zero on its own".
			if ctxErr := ctx.Err(); ctxErr != nil && t.status == StatusRunning {
				t.status = StatusKilled
			} else {
				t.status = StatusFailed
			}
		}
		close(t.doneCh)
		t.mu.Unlock()
		// Enqueue a notification for the engine. We grab the store
		// mutex AFTER releasing the task mutex to keep the lock
		// order consistent (store > task) — Stop walks store →
		// task → kill, so reversing here would risk a deadlock.
		s.markCompleted(t.ID)
	}()

	return t, nil
}

// drain copies the pipe line-by-line into the task's buffer.
// Tagged so consumers can see whether a line came from stdout or
// stderr (matches the prefix the foreground Bash tool emits).
func drain(wg *sync.WaitGroup, t *Task, r io.ReadCloser, prefix string) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if prefix == "stderr" {
			line = "[stderr] " + line
		}
		t.appendLine(line)
	}
}

// Get returns the task with the given ID. ok=false when not found.
func (s *Store) Get(id string) (*Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	return t, ok
}

// List returns a snapshot of every registered task in id order.
func (s *Store) List() []Snapshot {
	s.mu.Lock()
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.Unlock()
	out := make([]Snapshot, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.Snapshot())
	}
	// Stable id-order — matches user expectation of "first spawned
	// at the top". Numeric suffix sort, not lexical.
	sortByIDNumeric(out)
	return out
}

func sortByIDNumeric(s []Snapshot) {
	// Bubble sort is fine — len(s) is rarely > 20.
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if numericSuffix(s[i].ID) > numericSuffix(s[j].ID) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func numericSuffix(id string) int {
	// IDs are "bg-N" — strip the prefix and parse N. Falls back to 0
	// for malformed inputs (test-only edge case).
	if !strings.HasPrefix(id, "bg-") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "bg-"))
	if err != nil {
		return 0
	}
	return n
}

// Output reads buffered lines starting at sinceLine (0-based). The
// caller passes back the `next` value to fetch only new lines on
// subsequent polls (a delta-poll pattern).
//
// Returns:
//   - lines:     new content since `sinceLine`
//   - next:      pass this as the next sinceLine
//   - status:    current task status (so the model knows when to stop polling)
//   - dropped:   total lines lost to the buffer cap (so the model
//                knows when its sinceLine is older than the buffer head)
//
// When sinceLine < dropped, lines we've already discarded won't
// reappear; the model can detect this by seeing the gap.
func (s *Store) Output(id string, sinceLine int) (lines []string, next int, status Status, dropped int, ok bool) {
	t, found := s.Get(id)
	if !found {
		return nil, 0, "", 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// `lines` indexes into the *current* buffer (which has dropped
	// `t.dropped` lines). Translate the caller's absolute line
	// number into the local index.
	localStart := sinceLine - t.dropped
	if localStart < 0 {
		localStart = 0
	}
	if localStart > len(t.lines) {
		localStart = len(t.lines)
	}
	out := make([]string, len(t.lines)-localStart)
	copy(out, t.lines[localStart:])
	return out, sinceLine + len(out), t.status, t.dropped, true
}

// Stop sends SIGTERM, waits up to gracePeriod, then SIGKILL.
// Returns the final task snapshot. It's safe to call on an already-
// terminated task (no-op + return current state).
func (s *Store) Stop(id string) (Snapshot, error) {
	t, ok := s.Get(id)
	if !ok {
		return Snapshot{}, fmt.Errorf("bgtask: no task %q", id)
	}
	t.mu.Lock()
	if t.status != StatusRunning {
		snap := snapshotLocked(t)
		t.mu.Unlock()
		return snap, nil
	}
	// Mark as killed BEFORE signalling so the reaper goroutine sees
	// the user-initiated termination instead of misclassifying as
	// failed.
	t.status = StatusKilled
	pgid, _ := syscall.Getpgid(t.cmd.Process.Pid)
	t.mu.Unlock()

	// Best-effort SIGTERM to the entire process group, then escalate.
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = t.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-t.Done():
		// Process exited gracefully within the grace period.
	case <-time.After(gracePeriod):
		// Escalate. The cancel will trigger CommandContext's SIGKILL.
		t.mu.Lock()
		if t.cancel != nil {
			t.cancel()
		}
		t.mu.Unlock()
		// Block briefly until the reaper records the terminal state.
		<-t.Done()
	}
	return t.Snapshot(), nil
}

// snapshotLocked returns the snapshot assuming t.mu is already held.
// Internal helper — avoids re-locking inside Stop.
func snapshotLocked(t *Task) Snapshot {
	return Snapshot{
		ID: t.ID, Command: t.Command, Cwd: t.Cwd,
		Status:     t.status,
		StartedAt:  t.StartedAt,
		EndedAt:    t.endedAt,
		ExitCode:   t.exitCode,
		TotalLines: len(t.lines),
		Dropped:    t.dropped,
	}
}

// StopAll terminates every running task. Returns the count of
// tasks the call actively had to kill (already-terminated tasks
// don't count). Used by main / SDK on graceful shutdown.
func (s *Store) StopAll() int {
	s.mu.Lock()
	ids := make([]string, 0, len(s.tasks))
	for id, t := range s.tasks {
		t.mu.Lock()
		running := t.status == StatusRunning
		t.mu.Unlock()
		if running {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		_, _ = s.Stop(id)
	}
	return len(ids)
}

// nextID generates "bg-N" identifiers. Atomic counter so concurrent
// Spawn calls don't collide.
func (s *Store) nextID() string {
	n := atomic.AddInt64(&s.counter, 1)
	return "bg-" + strconv.FormatInt(n, 10)
}

// markCompleted records that `id` reached a terminal state. The
// engine drains this queue at the head of every user turn via
// Pending(). Safe under concurrent reaper goroutines.
func (s *Store) markCompleted(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// De-dup: a Stop() that fires AFTER the reaper has already
	// recorded the status would re-enqueue. Cheap linear scan
	// because pending is bounded by the number of tasks finishing
	// between two user turns (typically 0–3).
	for _, existing := range s.pending {
		if existing == id {
			return
		}
	}
	s.pending = append(s.pending, id)
}

// Pending returns snapshots of every task that completed since the
// last Pending call, then clears the queue atomically. Each
// snapshot includes a buffer-tail string so the engine can fold a
// preview into the system attachment without re-querying the store.
//
// Returns nil (not empty slice) when nothing is queued — lets the
// engine cheaply skip the attachment block with a `len() > 0` test.
func (s *Store) Pending() []Snapshot {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return nil
	}
	ids := s.pending
	s.pending = nil
	// Also pull the corresponding tasks while we still hold the
	// store mutex; that way a concurrent prune (future feature)
	// can't yank a task between the queue drain and the snapshot.
	tasks := make([]*Task, 0, len(ids))
	for _, id := range ids {
		if t, ok := s.tasks[id]; ok {
			tasks = append(tasks, t)
		}
	}
	s.mu.Unlock()

	out := make([]Snapshot, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.Snapshot())
	}
	return out
}

// Tail returns the last n captured lines for a task. Convenience
// for the engine's notification builder so it doesn't have to
// allocate-then-truncate the full Output() result. Returns nil for
// an unknown id; n ≤ 0 yields nil.
func (s *Store) Tail(id string, n int) []string {
	if n <= 0 {
		return nil
	}
	t, ok := s.Get(id)
	if !ok {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return nil
	}
	start := len(t.lines) - n
	if start < 0 {
		start = 0
	}
	out := make([]string, len(t.lines)-start)
	copy(out, t.lines[start:])
	return out
}
