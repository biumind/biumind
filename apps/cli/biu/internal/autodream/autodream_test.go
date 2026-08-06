package autodream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner counts invocations + records the last prompt.
type fakeRunner struct {
	mu      sync.Mutex
	calls   int
	last    string
	err     error
	delayMs int
}

func (f *fakeRunner) Run(ctx context.Context, prompt string) error {
	f.mu.Lock()
	f.calls++
	f.last = prompt
	delay := f.delayMs
	err := f.err
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
	return err
}

func (f *fakeRunner) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ─── Coordinator ─────────────────────────────────────────────

func TestNew_nilRunnerReturnsNil(t *testing.T) {
	if c := New(Config{Enabled: true}, nil); c != nil {
		t.Error("nil runner should produce nil coordinator")
	}
}

func TestNew_appliesDefaults(t *testing.T) {
	c := New(Config{Enabled: true}, &fakeRunner{})
	if c.cfg.MinHours != DefaultMinHours {
		t.Errorf("MinHours default = %d, want %d", c.cfg.MinHours, DefaultMinHours)
	}
	if c.cfg.MinSessions != DefaultMinSessions {
		t.Errorf("MinSessions default = %d, want %d", c.cfg.MinSessions, DefaultMinSessions)
	}
}

func TestMaybeRun_disabledIsNoOp(t *testing.T) {
	c := New(Config{Enabled: false}, &fakeRunner{})
	ran, err := c.MaybeRun(context.Background())
	if ran || err != nil {
		t.Errorf("disabled coordinator should be silent no-op, got ran=%v err=%v", ran, err)
	}
}

func TestMaybeRun_nilSafe(t *testing.T) {
	var c *Coordinator
	ran, err := c.MaybeRun(context.Background())
	if ran || err != nil {
		t.Error("nil receiver should never fire")
	}
}

func TestMaybeRun_timeGateBlocks(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	sessDir := filepath.Join(dir, "sessions")
	_ = os.MkdirAll(memDir, 0o755)
	_ = os.MkdirAll(sessDir, 0o755)
	// Pre-create a fresh lock file (mtime = now).
	lock := filepath.Join(memDir, ".consolidate-lock")
	_ = os.WriteFile(lock, []byte("0\n"), 0o644)

	runner := &fakeRunner{}
	c := New(Config{
		Enabled:     true,
		MinHours:    24,
		MinSessions: 1,
		MemoryDir:   memDir,
		SessionsDir: sessDir,
	}, runner)

	ran, err := c.MaybeRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Errorf("time gate should block; ran anyway")
	}
	if runner.Calls() != 0 {
		t.Errorf("runner should not be called on time-gate fail")
	}
}

func TestMaybeRun_sessionGateBlocks(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	sessDir := filepath.Join(dir, "sessions")
	_ = os.MkdirAll(memDir, 0o755)
	_ = os.MkdirAll(sessDir, 0o755)
	// No sessions touched → session gate fails.

	runner := &fakeRunner{}
	c := New(Config{
		Enabled:     true,
		MinHours:    1,
		MinSessions: 3,
		MemoryDir:   memDir,
		SessionsDir: sessDir,
	}, runner)

	ran, _ := c.MaybeRun(context.Background())
	if ran {
		t.Error("session gate should block when no sessions touched")
	}
	if runner.Calls() != 0 {
		t.Error("runner should not fire")
	}
}

func TestMaybeRun_allGatesPass(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	sessDir := filepath.Join(dir, "sessions")
	_ = os.MkdirAll(memDir, 0o755)
	_ = os.MkdirAll(sessDir, 0o755)
	// 3 session jsonl files with recent mtime.
	for _, n := range []string{"a.jsonl", "b.jsonl", "c.jsonl"} {
		_ = os.WriteFile(filepath.Join(sessDir, n), []byte("{}\n"), 0o644)
	}
	// No prior lock = lastConsolidatedAt = zero, so time gate
	// auto-passes and session gate sees all 3 files.

	runner := &fakeRunner{}
	c := New(Config{
		Enabled:     true,
		MinHours:    1,
		MinSessions: 3,
		MemoryDir:   memDir,
		SessionsDir: sessDir,
	}, runner)

	ran, err := c.MaybeRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("all gates passed → should run")
	}
	if runner.Calls() != 1 {
		t.Errorf("runner.Calls = %d, want 1", runner.Calls())
	}
	if !strings.Contains(runner.last, "Memory Consolidation") {
		t.Errorf("prompt missing header: %s", runner.last[:100])
	}
}

func TestMaybeRun_runnerErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	sessDir := filepath.Join(dir, "sessions")
	_ = os.MkdirAll(memDir, 0o755)
	_ = os.MkdirAll(sessDir, 0o755)
	_ = os.WriteFile(filepath.Join(sessDir, "a.jsonl"), []byte(""), 0o644)

	runner := &fakeRunner{err: errors.New("boom")}
	c := New(Config{
		Enabled:     true,
		MinHours:    0, // disabled gate; we want runner to be reachable
		MinSessions: 1,
		MemoryDir:   memDir,
		SessionsDir: sessDir,
	}, runner)
	c.cfg.MinHours = DefaultMinHours // cfg is set; override-after-New for clarity

	// Force the gate to pass: zero lastConsolidatedAt + 1 session.
	ran, err := c.MaybeRun(context.Background())
	if ran {
		t.Error("error should not flag ran")
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should propagate, got %v", err)
	}
}

func TestMaybeRun_lockMtimeUpdatedAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	sessDir := filepath.Join(dir, "sessions")
	_ = os.MkdirAll(memDir, 0o755)
	_ = os.MkdirAll(sessDir, 0o755)
	_ = os.WriteFile(filepath.Join(sessDir, "x.jsonl"), []byte(""), 0o644)

	runner := &fakeRunner{}
	c := New(Config{
		Enabled:     true,
		MinHours:    1,
		MinSessions: 1,
		MemoryDir:   memDir,
		SessionsDir: sessDir,
	}, runner)

	before := time.Now()
	if _, err := c.MaybeRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(memDir, ".consolidate-lock"))
	if err != nil {
		t.Fatal(err)
	}
	if st.ModTime().Before(before.Add(-time.Second)) {
		t.Errorf("lock mtime not updated; got %v vs %v", st.ModTime(), before)
	}
}

// ─── listSessionsTouchedSince ────────────────────────────────

func TestListSessionsTouchedSince_filtersByMtime(t *testing.T) {
	sessDir := t.TempDir()
	old := filepath.Join(sessDir, "old.jsonl")
	new1 := filepath.Join(sessDir, "new1.jsonl")
	new2 := filepath.Join(sessDir, "new2.jsonl")
	notSession := filepath.Join(sessDir, "ignore.txt")
	for _, p := range []string{old, new1, new2, notSession} {
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	// Push old's mtime back.
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(old, past, past)

	since := time.Now().Add(-1 * time.Hour)
	got, err := listSessionsTouchedSince(sessDir, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 fresh sessions, got %d (%v)", len(got), got)
	}
	for _, n := range got {
		if !strings.HasSuffix(n, ".jsonl") {
			t.Errorf("non-jsonl file leaked: %s", n)
		}
	}
}

// ─── prompt builder ──────────────────────────────────────────

func TestBuildConsolidationPrompt_includesPaths(t *testing.T) {
	got := BuildConsolidationPrompt("/tmp/mem", "/tmp/sess", []string{"a.jsonl", "b.jsonl"})
	for _, want := range []string{"/tmp/mem", "/tmp/sess", "Phase 1", "Phase 2", "Phase 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q: %s", want, got[:200])
		}
	}
}

func TestBuildConsolidationPrompt_capsTouchedList(t *testing.T) {
	var sessions []string
	for i := 0; i < 50; i++ {
		sessions = append(sessions, "session-X.jsonl")
	}
	got := BuildConsolidationPrompt("/m", "/s", sessions)
	if !strings.Contains(got, "showing 20 of 50") {
		t.Errorf("should cap to 20: %s", got[:300])
	}
}
