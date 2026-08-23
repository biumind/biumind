package repoapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMergeEnvOverride(t *testing.T) {
	env := mergeEnv([]string{"PORT=1111", "HOME=/x"}, map[string]string{"PORT": "2222", "NEW": "y"})
	got := map[string]string{}
	for _, kv := range env {
		for _, k := range []string{"PORT=", "HOME=", "NEW="} {
			if len(kv) > len(k) && kv[:len(k)] == k {
				got[k[:len(k)-1]] = kv[len(k):]
			}
		}
	}
	if got["PORT"] != "2222" {
		t.Errorf("runner-injected PORT must win over base env, got %q", got["PORT"])
	}
	if got["HOME"] != "/x" || got["NEW"] != "y" {
		t.Errorf("merge lost entries: %v", got)
	}
}

func TestWaitHealthy(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := waitHealthy(context.Background(), ok.URL, "/", 5*time.Second); err != nil {
		t.Errorf("healthy server: %v", err)
	}

	// 404 still counts as "the app is up" — the default health path is
	// just GET / and many apps don't serve it.
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()
	if err := waitHealthy(context.Background(), notFound.URL, "/", 5*time.Second); err != nil {
		t.Errorf("404 should count as alive: %v", err)
	}

	// 500 → retried until timeout.
	sick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer sick.Close()
	if err := waitHealthy(context.Background(), sick.URL, "/", 1500*time.Millisecond); err == nil {
		t.Error("500-forever server must fail the health check")
	}
}

func TestRunnerStopKillsAndCleansPID(t *testing.T) {
	if !Supported() {
		t.Skip("unix-only")
	}
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inst, err := store.Create("a-b")
	if err != nil {
		t.Fatal(err)
	}
	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	// Kill in cleanup as a backstop; Wait happens exactly once in the
	// background reaper (double Wait races under -race).
	t.Cleanup(func() { _ = sleep.Process.Kill() })
	go func() { _ = sleep.Wait() }() // reap promptly — zombies probe as alive
	if err := os.WriteFile(inst.PIDPath(), []byte(strconv.Itoa(sleep.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &Runner{Store: store}
	if pid, running := runner.Status("a-b"); !running || pid != sleep.Process.Pid {
		t.Fatalf("Status = %d,%v want live sleep pid", pid, running)
	}
	if err := runner.Stop("a-b"); err != nil {
		t.Fatal(err)
	}
	if _, running := runner.Status("a-b"); running {
		t.Error("instance should be stopped")
	}
	if _, err := os.Stat(inst.PIDPath()); !os.IsNotExist(err) {
		t.Error("pid file should be removed after Stop")
	}
}

func TestRunnerStopNonRunningIsNoop(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	if _, err := store.Create("x-y"); err != nil {
		t.Fatal(err)
	}
	if err := (&Runner{Store: store}).Stop("x-y"); err != nil {
		t.Errorf("stopping a non-running instance must be a no-op: %v", err)
	}
}

func TestRunnerLogs(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	inst, err := store.Create("l-m")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inst.LogPath(), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &strings.Builder{}
	if err := (&Runner{Store: store}).Logs(context.Background(), "l-m", false, out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "line1\nline2\n" {
		t.Errorf("logs = %q", out.String())
	}
	// Missing log → helpful error.
	if err := (&Runner{Store: store}).Logs(context.Background(), "nope", false, out); err == nil {
		t.Error("missing log should error")
	}
}
