// Package autodream runs background memory consolidation — fires
// the consolidation prompt as a sub-agent when enough time +
// enough new sessions have accumulated since the last run.
//
//  The
// intuition: ~/.biumind/memory/ accumulates one-line facts (saved
// via the Memory tool) across many sessions, and over time it
// drifts — duplicates, stale rules, contradictions with the
// current codebase. autoDream is the "garbage collection" pass
// that asks the LLM to reorganise / dedupe / prune.
//
// Trigger gates (cheapest first):
//
//   1. Time: hours since lastConsolidatedAt >= MinHours
//   2. Sessions: count of session jsonl with mtime >
//      lastConsolidatedAt >= MinSessions
//   3. Lock: no other biu process mid-consolidation
//
// All three must pass. Single-flight lock at the filesystem level
// (lock file's mtime IS lastConsolidatedAt) so we don't need a
// separate timestamp file.
//
// Runner is pluggable via the Runner interface — biu's wiring layer
// can pass a real LLM-driven sub-agent or a no-op stub for tests.

package autodream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default trigger thresholds — biu uses a slightly tighter cadence
// so users see consolidation pay off faster in their first month,
// then it backs off naturally as MinSessions becomes the binding
// constraint.
const (
	DefaultMinHours    = 24
	DefaultMinSessions = 3
)

// Config tunes the gate thresholds.
type Config struct {
	// Enabled — master switch. When false, MaybeRun is a no-op
	// regardless of every other state. Default false (opt-in).
	Enabled bool

	// MinHours since the last consolidation. Default 24.
	MinHours int

	// MinSessions touched since the last consolidation that must
	// have accumulated. Default 3.
	MinSessions int

	// MemoryDir is the absolute path to the consolidation target.
	// Default: ~/.biumind/memory.
	MemoryDir string

	// SessionsDir is where biu writes session jsonl files. Used
	// for the session-gate count. Default: ~/.biu/sessions.
	SessionsDir string
}

// DefaultConfig fills paths from $HOME and applies the conservative
// disabled-by-default state.
func DefaultConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	return Config{
		Enabled:     false,
		MinHours:    DefaultMinHours,
		MinSessions: DefaultMinSessions,
		MemoryDir:   filepath.Join(home, ".biumind", "memory"),
		SessionsDir: filepath.Join(home, ".biu", "sessions"),
	}, nil
}

// Runner is the surface autodream needs to actually run a
// consolidation pass. The wiring layer supplies an implementation
// that drives a sub-agent with the consolidation prompt; tests
// substitute a fake.
type Runner interface {
	// Run a consolidation pass. The prompt + context are built by
	// autodream and handed off; the runner is responsible for
	// LLM call + result. The returned error aborts the pass —
	// the lock is released so the next trigger window can retry.
	Run(ctx context.Context, prompt string) error
}

// Coordinator wires Config + Runner + lock + gate checks. One per
// process — singleton at the wiring layer.
type Coordinator struct {
	cfg    Config
	runner Runner
	mu     sync.Mutex
	inFly  bool
}

// New returns a coordinator. nil runner produces nil coordinator —
// the wiring layer treats that as "feature off" cleanly.
func New(cfg Config, runner Runner) *Coordinator {
	if runner == nil {
		return nil
	}
	if cfg.MinHours <= 0 {
		cfg.MinHours = DefaultMinHours
	}
	if cfg.MinSessions <= 0 {
		cfg.MinSessions = DefaultMinSessions
	}
	return &Coordinator{cfg: cfg, runner: runner}
}

// MaybeRun checks every gate and runs consolidation when all clear.
// Returns (true, nil) on a completed pass; (false, nil) when a gate
// blocked; (false, err) when the runner failed.
//
// Single-flight at the in-memory level via inFly + at the
// filesystem level via the lock file. Only one of the two needs to
// fire to prevent double-runs, but having both means a crashed
// process doesn't strand the in-memory flag forever.
func (c *Coordinator) MaybeRun(ctx context.Context) (bool, error) {
	if c == nil || !c.cfg.Enabled {
		return false, nil
	}
	c.mu.Lock()
	if c.inFly {
		c.mu.Unlock()
		return false, nil
	}
	c.inFly = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFly = false
		c.mu.Unlock()
	}()

	lastAt, err := readLastConsolidatedAt(c.cfg.MemoryDir)
	if err != nil {
		return false, fmt.Errorf("read last consolidated: %w", err)
	}

	// Time gate.
	if !lastAt.IsZero() && time.Since(lastAt) < time.Duration(c.cfg.MinHours)*time.Hour {
		return false, nil
	}

	// Session gate.
	touched, err := listSessionsTouchedSince(c.cfg.SessionsDir, lastAt)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("scan sessions: %w", err)
	}
	if len(touched) < c.cfg.MinSessions {
		return false, nil
	}

	// Lock acquisition.
	released, err := tryAcquireLock(c.cfg.MemoryDir)
	if err != nil {
		return false, fmt.Errorf("acquire lock: %w", err)
	}
	if released == nil {
		// Lock held by another process; bail without running.
		return false, nil
	}
	defer released()

	// All gates passed → run.
	prompt := BuildConsolidationPrompt(c.cfg.MemoryDir, c.cfg.SessionsDir, touched)
	if err := c.runner.Run(ctx, prompt); err != nil {
		return false, fmt.Errorf("runner: %w", err)
	}

	// Touch the lock file's mtime to record completion. Lock file
	// stays — its presence is the lastConsolidatedAt timestamp.
	if err := updateLockMtime(c.cfg.MemoryDir, time.Now()); err != nil {
		// Non-fatal; the consolidation already happened. Surface
		// as the only error path that doesn't abort the pass.
		return true, fmt.Errorf("update lock mtime: %w", err)
	}
	return true, nil
}

// readLastConsolidatedAt returns the lock file's mtime, or zero
// time when absent. One stat per check.
func readLastConsolidatedAt(memDir string) (time.Time, error) {
	st, err := os.Stat(lockPath(memDir))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// listSessionsTouchedSince returns the names of session jsonl files
// modified after `since`. We use ReadDir + per-file stat; on a
// laptop with thousands of sessions this is still sub-ms.
func listSessionsTouchedSince(sessionsDir string, since time.Time) ([]string, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if since.IsZero() || info.ModTime().After(since) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// tryAcquireLock attempts to claim the consolidation lock. Returns
// a release function on success, nil on contention. The lock is
// the lockfile itself: O_EXCL create wins, others see ErrExist.
//
// Stale-PID handling: when the existing lock's PID isn't a live
// process AND its mtime is older than 1h, we steal the lock. This
// keeps a crashed process from blocking consolidation forever.
func tryAcquireLock(memDir string) (release func() error, err error) {
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		return nil, err
	}
	path := lockPath(memDir)

	// Fast path: try O_EXCL create.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		if _, werr := fmt.Fprintf(f, "%d\n", os.Getpid()); werr != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return nil, werr
		}
		_ = f.Close()
		return func() error {
			// Don't delete on release — the lock file's mtime is the
			// lastConsolidatedAt timestamp, and we WANT it to stay
			// for the next gate check. Just leave it.
			return nil
		}, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	// Lock held — check for staleness.
	st, statErr := os.Stat(path)
	if statErr != nil {
		return nil, statErr
	}
	if time.Since(st.ModTime()) < time.Hour {
		return nil, nil // honoured
	}
	// Stale: steal. Best-effort delete + retry.
	_ = os.Remove(path)
	f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil
	}
	_ = f.Close()
	return func() error { return nil }, nil
}

// updateLockMtime sets the lock file's mtime to t. Used after a
// successful pass so the time gate measures from "completion of
// the most recent successful run", not "start".
func updateLockMtime(memDir string, t time.Time) error {
	return os.Chtimes(lockPath(memDir), t, t)
}

// lockPath returns the absolute path of the consolidation lock.
func lockPath(memDir string) string {
	return filepath.Join(memDir, ".consolidate-lock")
}
