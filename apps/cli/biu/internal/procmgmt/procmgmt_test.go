package procmgmt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// deadPID returns a pid that is guaranteed not to be running: spawn a
// trivially-exiting process and reap it, so the pid is gone.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawn true: %v", err)
	}
	return cmd.Process.Pid
}

func TestReadPIDRoundtrip(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "x.pid")
	if err := os.WriteFile(pf, []byte("1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, ok := ReadPID(pf)
	if !ok || pid != 1234 {
		t.Errorf("ReadPID = %d,%v want 1234,true", pid, ok)
	}
	if _, ok := ReadPID(filepath.Join(t.TempDir(), "missing.pid")); ok {
		t.Error("missing pid file should report ok=false")
	}
	garbage := filepath.Join(t.TempDir(), "g.pid")
	if err := os.WriteFile(garbage, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadPID(garbage); ok {
		t.Error("garbage pid file should report ok=false")
	}
}

func TestProcessAliveSelfAndDead(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Error("own pid must be alive")
	}
	if ProcessAlive(deadPID(t)) {
		t.Error("reaped pid must not be alive")
	}
	if ProcessAlive(0) || ProcessAlive(-3) {
		t.Error("non-positive pids must not be alive")
	}
}

func TestAcquirePIDFileStaleOverwrite(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "x.pid")
	if err := os.WriteFile(pf, []byte(strconv.Itoa(deadPID(t))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AcquirePIDFile(pf, nil, "a test process"); err != nil {
		t.Fatalf("stale pid should be overwritten, got: %v", err)
	}
	got, _ := os.ReadFile(pf)
	if strings.TrimSpace(string(got)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("pid file not rewritten with own pid: %q", got)
	}
}

func TestAcquirePIDFileRefusesLiveForeign(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "x.pid")
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	t.Cleanup(func() { _ = sleep.Process.Kill(); _ = sleep.Wait() })
	if err := os.WriteFile(pf, []byte(strconv.Itoa(sleep.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Nil reclaim predicate → any live holder blocks acquisition, and the
	// holder must survive (never kill an unrelated process).
	if err := AcquirePIDFile(pf, nil, "a test process"); err == nil {
		t.Fatal("expected refusal when a live process holds the pid file")
	}
	if !ProcessAlive(sleep.Process.Pid) {
		t.Error("foreign holder must not be killed")
	}
}

func TestAcquirePIDFileReclaimsWithPredicate(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "x.pid")
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	// Kill in cleanup as a backstop; Wait happens exactly once in the
	// background reaper (double Wait races under -race).
	t.Cleanup(func() { _ = sleep.Process.Kill() })
	// Reap in the background so the terminated child doesn't linger as a
	// zombie (signal-0 probing counts zombies as alive).
	go func() { _ = sleep.Wait() }()
	holder := sleep.Process.Pid
	if err := os.WriteFile(pf, []byte(strconv.Itoa(holder)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AcquirePIDFile(pf, func(pid int) bool { return pid == holder }, "a test process"); err != nil {
		t.Fatalf("reclaimable holder should be terminated and replaced: %v", err)
	}
	// TerminatePID gives SIGTERM a grace window; poll briefly.
	deadline := time.Now().Add(3 * time.Second)
	for ProcessAlive(holder) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if ProcessAlive(holder) {
		t.Error("reclaimed holder should be dead")
	}
}

func TestTerminatePIDKills(t *testing.T) {
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	// Kill in cleanup as a backstop; Wait happens exactly once in the
	// background reaper (double Wait races under -race).
	t.Cleanup(func() { _ = sleep.Process.Kill() })
	go func() { _ = sleep.Wait() }() // reap promptly — zombies probe as alive
	TerminatePID(sleep.Process.Pid)
	if ProcessAlive(sleep.Process.Pid) {
		t.Error("sleep should be gone after TerminatePID")
	}
}

func TestReleasePIDFile(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "x.pid")
	if err := os.WriteFile(pf, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ReleasePIDFile(pf)
	if _, err := os.Stat(pf); !os.IsNotExist(err) {
		t.Error("pid file should be removed")
	}
	ReleasePIDFile(pf) // idempotent: no panic on missing file
}
