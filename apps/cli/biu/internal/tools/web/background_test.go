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

// flattenBg returns the joined text content of a tool result, in
// reading order. (Background-test-local — `flatten` already exists
// in web_test.go for the search-tool tests.)
func flattenBg(p *engine.ToolResultPayload) string {
	var b strings.Builder
	for _, c := range p.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// ─── Bash{run_in_background:true} ───────────────────────

func TestBashBackgroundReturnsTaskID(t *testing.T) {
	store := bgtask.NewStore()
	defer store.StopAll()

	tool := BashTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"command":           "sleep 2",
		"run_in_background": true,
	}, &engine.ToolEnv{})
	if out.IsError {
		t.Fatalf("background spawn errored: %+v", out)
	}
	body := flattenBg(out)
	if !strings.Contains(body, "Background task started with id=bg-1") {
		t.Errorf("status line should advertise task id; got %q", body)
	}
	// Spawn must NOT have blocked on the sleep — total time should
	// be well under 2s.
	if len(store.List()) != 1 {
		t.Errorf("expected 1 task in store; got %d", len(store.List()))
	}
}

func TestBashBackgroundWithoutStoreSoftErrors(t *testing.T) {
	tool := BashTool{} // no BgTasks
	out, _ := tool.Call(context.Background(), map[string]any{
		"command":           "true",
		"run_in_background": true,
	}, &engine.ToolEnv{})
	if !out.IsError {
		t.Errorf("missing store should soft-error; got %+v", out)
	}
}

// Sanity: foreground path is unchanged when run_in_background is
// absent or false. Catches accidental regression of the legacy code
// path when adding the bg branch.
func TestBashForegroundUnchangedWithoutFlag(t *testing.T) {
	store := bgtask.NewStore()
	tool := BashTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"command": `printf "fg\n"`,
	}, &engine.ToolEnv{})
	if out.IsError {
		t.Fatalf("foreground failed: %+v", out)
	}
	if !strings.Contains(flattenBg(out), "fg") {
		t.Errorf("foreground stdout missing: %q", flattenBg(out))
	}
	// Foreground must not have created a background task.
	if got := len(store.List()); got != 0 {
		t.Errorf("foreground should NOT register a bg task; got %d", got)
	}
}

// ─── BashOutput ─────────────────────────────────────────

func TestBashOutputReadsCapturedLines(t *testing.T) {
	store := bgtask.NewStore()
	defer store.StopAll()

	// Spawn directly so the test doesn't depend on Bash tool plumbing.
	task, err := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: `printf "alpha\nbeta\ngamma\n"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-task.Done()

	tool := BashOutputTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": task.ID,
	}, nil)
	if out.IsError {
		t.Fatalf("BashOutput failed: %+v", out)
	}
	body := flattenBg(out)
	for _, want := range []string{"status: done", "next_line: 3", "alpha", "beta", "gamma"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got %q", want, body)
		}
	}
}

func TestBashOutputDeltaSinceLine(t *testing.T) {
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: `printf "1\n2\n3\n4\n5\n"`,
	})
	<-task.Done()

	tool := BashOutputTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id":    task.ID,
		"since_line": 3,
	}, nil)
	body := flattenBg(out)
	if !strings.Contains(body, "since_line: 3") {
		t.Errorf("since_line not echoed: %q", body)
	}
	if !strings.Contains(body, "next_line: 5") {
		t.Errorf("next_line: got %q", body)
	}
	// Lines 1-3 must NOT appear; lines 4-5 must.
	if strings.Contains(body, "\n1\n") || strings.Contains(body, "\n2\n") {
		t.Errorf("delta poll leaked older lines: %q", body)
	}
	if !strings.Contains(body, "\n4\n") || !strings.Contains(body, "\n5\n") {
		t.Errorf("delta poll missing new lines: %q", body)
	}
}

func TestBashOutputUnknownTaskSoftErrors(t *testing.T) {
	store := bgtask.NewStore()
	tool := BashOutputTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": "bg-9999",
	}, nil)
	if !out.IsError {
		t.Errorf("unknown task should soft-error; got %+v", out)
	}
}

func TestBashOutputRequiresTaskID(t *testing.T) {
	tool := BashOutputTool{BgTasks: bgtask.NewStore()}
	out, _ := tool.Call(context.Background(), map[string]any{}, nil)
	if !out.IsError || !strings.Contains(out.SoftError, "task_id") {
		t.Errorf("missing task_id should soft-error mentioning the field; got %+v", out)
	}
}

// ─── KillBash ───────────────────────────────────────────

func TestKillBashStopsRunningTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals only")
	}
	store := bgtask.NewStore()
	defer store.StopAll()

	task, _ := store.Spawn(context.Background(), bgtask.SpawnRequest{
		Command: "sleep 30",
	})
	time.Sleep(50 * time.Millisecond)

	tool := KillBashTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": task.ID,
	}, nil)
	if out.IsError {
		t.Fatalf("KillBash failed: %+v", out)
	}
	body := flattenBg(out)
	if !strings.Contains(body, "status: killed") {
		t.Errorf("expected `status: killed` in payload; got %q", body)
	}
}

func TestKillBashUnknownTaskSoftErrors(t *testing.T) {
	store := bgtask.NewStore()
	tool := KillBashTool{BgTasks: store}
	out, _ := tool.Call(context.Background(), map[string]any{
		"task_id": "bg-9999",
	}, nil)
	if !out.IsError {
		t.Errorf("unknown task should soft-error; got %+v", out)
	}
}

// ─── Register integration ───────────────────────────────

// Register must add BashOutput / KillBash only when a store is
// supplied — keeps the tool catalog faithful to what's actually
// usable.
func TestRegisterAddsBackgroundToolsOnlyWithStore(t *testing.T) {
	regWithoutStore := engine.NewRegistry()
	installed := Register(regWithoutStore, Options{})
	for _, banned := range []string{"BashOutput", "KillBash"} {
		for _, n := range installed {
			if n == banned {
				t.Errorf("%s should NOT be installed without a store", banned)
			}
		}
	}

	regWithStore := engine.NewRegistry()
	installed = Register(regWithStore, Options{BgTasks: bgtask.NewStore()})
	for _, want := range []string{"BashOutput", "KillBash"} {
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
