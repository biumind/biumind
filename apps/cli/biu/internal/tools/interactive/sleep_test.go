package interactive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

func TestSleepTool_basicWait(t *testing.T) {
	start := time.Now()
	res, err := SleepTool{}.Call(context.Background(),
		map[string]any{"duration_seconds": 0.05}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed < 40*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("elapsed = %s, want ~50ms", elapsed)
	}
	if res == nil || res.IsError {
		t.Errorf("result should be successful: %+v", res)
	}
	if !strings.Contains(textOf(res), "Waited") {
		t.Errorf("body = %q", textOf(res))
	}
}

func TestSleepTool_zeroDurationOK(t *testing.T) {
	res, err := SleepTool{}.Call(context.Background(),
		map[string]any{"duration_seconds": 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("zero duration should succeed: %+v", res)
	}
}

func TestSleepTool_missingDuration(t *testing.T) {
	res, _ := SleepTool{}.Call(context.Background(), map[string]any{}, nil)
	if res == nil || !res.IsError {
		t.Error("missing duration should soft-error")
	}
}

func TestSleepTool_negativeDuration(t *testing.T) {
	res, _ := SleepTool{}.Call(context.Background(),
		map[string]any{"duration_seconds": -1.0}, nil)
	if res == nil || !res.IsError {
		t.Error("negative duration should soft-error")
	}
}

func TestSleepTool_clampsExcessive(t *testing.T) {
	// Don't actually wait MaxSleepDuration; use a context that
	// cancels immediately so we observe the clamp message via the
	// cancellation path. Verify clamp happens by checking the
	// would-be duration logic via reflection on the formatted
	// output. Easier: directly Call with a 2h request + an
	// already-cancelled ctx — the clamp message lives in the
	// formatted result.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := SleepTool{}.Call(ctx,
		map[string]any{"duration_seconds": 7200.0}, nil)
	if res == nil || !res.IsError {
		t.Fatal("cancelled context should soft-error")
	}
	if !strings.Contains(textOf(res), "1h0m") {
		t.Errorf("error message should mention the clamp target (1h), got %q",
			textOf(res))
	}
}

func TestSleepTool_respectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res, _ := SleepTool{}.Call(ctx,
		map[string]any{"duration_seconds": 5.0}, nil)
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Errorf("ctx cancel didn't unblock; elapsed = %s", elapsed)
	}
	if !res.IsError {
		t.Error("interrupted Sleep should soft-error")
	}
}

func TestSleepTool_concurrencySafeFlag(t *testing.T) {
	if !(SleepTool{}.IsConcurrencySafe(nil)) {
		t.Error("Sleep must be concurrency-safe")
	}
	if !(SleepTool{}.IsReadOnly(nil)) {
		t.Error("Sleep should be read-only")
	}
	if (SleepTool{}).IsDestructive(nil) {
		t.Error("Sleep is not destructive")
	}
}

// textOf flattens a ToolResultPayload's text content for assertion.
func textOf(r *engine.ToolResultPayload) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	if r.SoftError != "" {
		b.WriteString(r.SoftError)
	}
	return b.String()
}
