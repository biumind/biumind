// Package procmgmt holds the PID-file lifecycle helpers shared by
// long-lived child processes biu manages (`biu serve` daemon, repo-app
// runners, ...).
//
// Extracted from cmd/biu/serve_cmd.go so both call sites share one
// implementation. The semantics are unchanged from the original:
//
//   - stale pid file (holder dead)        → overwrite
//   - live holder the caller can reclaim  → SIGTERM/SIGKILL it, then take over
//   - live holder the caller may NOT kill → refuse (never kill an unrelated
//     process that happened to reuse the pid)
//
// Unix-only semantics (signal 0 probing, SIGTERM/SIGKILL). The functions
// compile cross-platform but are only exercised on macOS/Linux today.
package procmgmt

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// AcquirePIDFile writes the current PID to path, resolving any existing
// holder by state:
//
//   - stale (holder process dead) → overwrite.
//   - alive and canReclaim(pid)   → terminate it and take over (hot
//     restart / reclaiming a leaked instance).
//   - alive and !canReclaim(pid)  → refuse; never kill an unrelated
//     process that reused the pid.
//
// canReclaim may be nil, in which case any live holder blocks
// acquisition. kind names the reclaimable process kind for error/log
// messages (e.g. "a biu serve"), keeping refusal output specific about
// what the file was expected to belong to.
func AcquirePIDFile(path string, canReclaim func(pid int) bool, kind string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if existing, err := os.ReadFile(path); err == nil {
		pidStr := strings.TrimSpace(string(existing))
		if pid, perr := strconv.Atoi(pidStr); pidStr != "" && perr == nil {
			switch {
			case !ProcessAlive(pid):
				fmt.Fprintf(os.Stderr, "[biu] procmgmt: clearing stale pid file %s (pid=%d not running)\n",
					path, pid)
			case canReclaim != nil && canReclaim(pid):
				fmt.Fprintf(os.Stderr,
					"[biu] procmgmt: reclaiming pid file %s from live %s pid=%d (likely leaked from a prior session)\n",
					path, kind, pid)
				TerminatePID(pid)
				if ProcessAlive(pid) {
					return fmt.Errorf("procmgmt: pid file %s held by pid=%d which did not exit after SIGTERM/SIGKILL", path, pid)
				}
			default:
				return fmt.Errorf("procmgmt: pid file %s held by live pid=%d that is not %s; refusing to kill", path, pid, kind)
			}
		}
	}
	pid := os.Getpid()
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// ReleasePIDFile removes the PID file. Intended for defer; errors are
// only logged.
func ReleasePIDFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[biu] procmgmt: failed to remove pid file: %v\n", err)
	}
}

// ReadPID parses a pid file. Missing / unparseable files return ok=false
// so callers can treat "no file" and "garbage file" the same.
func ReadPID(path string) (pid int, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// TerminatePID gracefully terminates pid: SIGTERM, wait up to ~2s, then
// SIGKILL. Kept as the 2s default for the original `biu serve` behaviour;
// callers needing a different grace period use TerminatePIDTimeout.
func TerminatePID(pid int) {
	TerminatePIDTimeout(pid, 2*time.Second)
}

// TerminatePIDTimeout is TerminatePID with an explicit SIGTERM grace
// period before the SIGKILL escalation.
func TerminatePIDTimeout(pid int, grace time.Duration) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = p.Signal(syscall.SIGKILL)
}

// ProcessAlive probes a pid with signal 0 (no actual signal delivered,
// just an existence/permission check). Unix-only semantics; see the
// package doc.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}
