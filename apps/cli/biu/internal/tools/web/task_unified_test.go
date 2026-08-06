// Tests for TaskOutput / TaskStop. These mirror background_test.go's
// fixtures (direct store.Spawn rather than going through Bash) so a
// regression in the bash wrapper doesn't show up here.

package web

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// ─── TaskOutput ─────────────────────────────────────────

func TestTaskOutputBlocksUntilDone(t *testing.T) {
	store := bgtask.NewStore()
	defer store.StopAll()

	task, err := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: `printf "alpha\nbeta\n"`,
	})
	if err != nil {
		t.Fatal(err)
	}

	tool := TaskOutputTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": task.ID,
		"block":   true,
		"timeout": 5000,
	}, nil)
	if out.IsError {
		t.Fatalf("TaskOutput errored: %+v", out)
	}
	body := flattenBg(out)
	if !strings.Contains(body, "retrieval_status: success") {
		t.Errorf("blocking call should report success; got %q", body)
	}
	if !strings.Contains(body, "status: done") {
		t.Errorf("blocked task should be done; got %q", body)
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q in %q", want, body)
		}
	}
	if !strings.Contains(body, "task_type: local_bash") {
		t.Errorf("type label missing; got %q", body)
	}
}

func TestTaskOutputNonBlockingReportsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep not portable")
	}
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: "sleep 5",
	})
	// Tiny pause so the spawn has actually flipped to running before
	// we poll. Without it the goroutine that flips status may not
	// have run yet.
	time.Sleep(20 * time.Millisecond)

	tool := TaskOutputTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": task.ID,
		"block":   false,
	}, nil)
	if out.IsError {
		t.Fatalf("TaskOutput errored: %+v", out)
	}
	body := flattenBg(out)
	if !strings.Contains(body, "retrieval_status: not_ready") {
		t.Errorf("non-block on running task should be not_ready; got %q", body)
	}
	if !strings.Contains(body, "status: running") {
		t.Errorf("expected running status; got %q", body)
	}
}

func TestTaskOutputBlockTimesOutOnLongRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep not portable")
	}
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: "sleep 30",
	})

	tool := TaskOutputTool{BgTasks: store}
	start := time.Now()
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": task.ID,
		"block":   true,
		"timeout": 300, // 0.3s — well under the sleep
	}, nil)
	elapsed := time.Since(start)

	if out.IsError {
		t.Fatalf("TaskOutput errored: %+v", out)
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout should fire promptly; took %v", elapsed)
	}
	body := flattenBg(out)
	if !strings.Contains(body, "retrieval_status: timeout") {
		t.Errorf("timeout case should be flagged; got %q", body)
	}
}

func TestTaskOutputUnknownTaskSoftErrors(t *testing.T) {
	store := bgtask.NewStore()
	tool := TaskOutputTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": "bg-9999",
	}, nil)
	if !out.IsError {
		t.Errorf("unknown task should soft-error; got %+v", out)
	}
}

func TestTaskOutputRequiresTaskID(t *testing.T) {
	tool := TaskOutputTool{BgTasks: bgtask.NewStore()}
	out, _ := tool.Call(context.Background(), map[string]any{}, nil)
	if !out.IsError {
		t.Errorf("missing task_id should soft-error; got %+v", out)
	}
}

// Timeout above the schema cap is clamped, not rejected. A model
// that emits 3_600_000 by mistake should still get a usable result.
func TestTaskOutputTimeoutClampsAtMax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep not portable")
	}
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: `printf done\\n`,
	})
	tool := TaskOutputTool{BgTasks: store}

	// We can't actually wait taskOutputMaxTimeoutMs; what we want to
	// verify is that the call still completes (the task is fast). If
	// the clamp were broken into a reject, this would soft-error.
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": task.ID,
		"block":   true,
		"timeout": 9_999_999, // > max
	}, nil)
	if out.IsError {
		t.Fatalf("oversized timeout should clamp, not reject; got %+v", out)
	}
}

// ─── TaskStop ───────────────────────────────────────────

func TestTaskStopByTaskID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals only")
	}
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: "sleep 30",
	})
	time.Sleep(50 * time.Millisecond)

	tool := TaskStopTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": task.ID,
	}, nil)
	if out.IsError {
		t.Fatalf("TaskStop errored: %+v", out)
	}
	body := flattenBg(out)
	if !strings.Contains(body, "status: killed") {
		t.Errorf("expected killed status; got %q", body)
	}
	if !strings.Contains(body, "task_type: local_bash") {
		t.Errorf("expected task_type label; got %q", body)
	}
}

// shell_id is the deprecated alias from the KillShell era. Prompts
// frozen against that name must still function.
func TestTaskStopAcceptsShellIDAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals only")
	}
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: "sleep 30",
	})
	time.Sleep(50 * time.Millisecond)

	tool := TaskStopTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"shell_id": task.ID, // deprecated key
	}, nil)
	if out.IsError {
		t.Fatalf("shell_id alias should still work; got %+v", out)
	}
}

func TestTaskStopUnknownIDSoftErrors(t *testing.T) {
	tool := TaskStopTool{BgTasks: bgtask.NewStore()}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": "bg-9999",
	}, nil)
	if !out.IsError {
		t.Errorf("unknown id should soft-error; got %+v", out)
	}
}

func TestTaskStopRequiresAnyID(t *testing.T) {
	tool := TaskStopTool{BgTasks: bgtask.NewStore()}
	out, _ := tool.Call(context.Background(), map[string]any{}, nil)
	if !out.IsError {
		t.Errorf("missing both ids should soft-error; got %+v", out)
	}
}

// ─── Register integration ───────────────────────────────

// Register adds the unified pair only when the store is wired —
// matches the BashOutput/KillBash gating logic.
func TestRegisterAddsTaskUnifiedToolsOnlyWithStore(t *testing.T) {
	regWithoutStore := engine.NewRegistry()
	installed := Register(regWithoutStore, Options{})
	for _, banned := range []string{"TaskOutput", "TaskStop"} {
		for _, n := range installed {
			if n == banned {
				t.Errorf("%s should NOT be installed without a store", banned)
			}
		}
	}

	regWithStore := engine.NewRegistry()
	installed = Register(regWithStore, Options{BgTasks: bgtask.NewStore()})
	for _, want := range []string{"TaskOutput", "TaskStop"} {
		found := false
		for _, n := range installed {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s should be installed when store is set; installed=%v", want, installed)
		}
	}
}
