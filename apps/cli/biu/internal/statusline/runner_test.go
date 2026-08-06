package statusline

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenderNilRunnerSafe(t *testing.T) {
	var r *Runner
	if got := r.Render(context.Background(), Input{}); got != "" {
		t.Errorf("nil runner should return empty; got %q", got)
	}
}

func TestRenderEmptyCommandSafe(t *testing.T) {
	r := New(Config{})
	if got := r.Render(context.Background(), Input{}); got != "" {
		t.Errorf("empty command should return empty; got %q", got)
	}
}

func TestRenderHappyPathReturnsFirstLine(t *testing.T) {
	r := New(Config{Command: `printf "branch: main\nignored second line\n"`})
	got := r.Render(context.Background(), Input{Model: "claude-opus-4-7"})
	if got != "branch: main" {
		t.Errorf("got %q, want first line `branch: main`", got)
	}
}

func TestRenderPipesInputAsJSONOnStdin(t *testing.T) {
	// The script echoes back the model field from its stdin JSON,
	// proving the runner's stdin pipe carries the structured input.
	r := New(Config{
		Command: `python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["model"])'`,
	})
	got := r.Render(context.Background(), Input{Model: "claude-haiku-4-5"})
	if got != "claude-haiku-4-5" {
		t.Errorf("script didn't see model on stdin; got %q", got)
	}
}

func TestRenderHidesNonZeroExits(t *testing.T) {
	r := New(Config{Command: `printf "would-show\n"; exit 1`})
	if got := r.Render(context.Background(), Input{}); got != "" {
		t.Errorf("non-zero exit should yield empty; got %q", got)
	}
}

func TestRenderHidesTimeouts(t *testing.T) {
	r := New(Config{
		Command: `sleep 5`,
		Timeout: 100 * time.Millisecond,
	})
	start := time.Now()
	got := r.Render(context.Background(), Input{})
	if got != "" {
		t.Errorf("timed-out script should yield empty; got %q", got)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("timeout should fire fast; took %s", took)
	}
}

// Hot-path: the cache returns the previous good value without re-
// forking until the refresh interval elapses. We verify by counting
// invocations through a side-effecting script.
func TestRenderCachesWithinRefreshWindow(t *testing.T) {
	tmp := t.TempDir()
	counter := tmp + "/n"
	r := New(Config{
		// Every run appends an 'x' to a counter file then prints
		// the file's length — gives us both visibility into call
		// count and a stable status string for cache hits.
		Command: `printf x >> ` + counter + `; wc -c < ` + counter,
		Refresh: 500 * time.Millisecond,
	})
	// First call: cold start, runs the script.
	first := strings.TrimSpace(r.Render(context.Background(), Input{}))
	if first != "1" {
		t.Fatalf("first render should produce '1'; got %q", first)
	}
	// Three rapid follow-up calls within the refresh window — must
	// all hit the cache (counter still says 1).
	for i := 0; i < 3; i++ {
		got := strings.TrimSpace(r.Render(context.Background(), Input{}))
		if got != "1" {
			t.Errorf("render %d hit script (got %q); should reuse cache", i, got)
		}
	}
	// Wait past the refresh window and the next render should re-fork.
	time.Sleep(600 * time.Millisecond)
	got := strings.TrimSpace(r.Render(context.Background(), Input{}))
	if got != "2" {
		t.Errorf("post-window render should refresh; got %q", got)
	}
}

// Concurrent renders in the same refresh window must not stampede
// the script: at most one fork executes, the rest see the cached
// (or in-flight) result.
func TestRenderSingleFlightUnderConcurrency(t *testing.T) {
	tmp := t.TempDir()
	counter := tmp + "/n"
	r := New(Config{
		Command: `printf x >> ` + counter + `; sleep 0.1; wc -c < ` + counter,
		Refresh: 10 * time.Second, // long → strictly cache after first
	})
	// Warm the cache with one foreground call.
	first := strings.TrimSpace(r.Render(context.Background(), Input{}))
	if first != "1" {
		t.Fatalf("warmup should be 1; got %q", first)
	}
	// Now blast 50 concurrent renders. None should re-fork because
	// the cache is fresh AND the refresh window is huge.
	var wg sync.WaitGroup
	var hits int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := strings.TrimSpace(r.Render(context.Background(), Input{}))
			if out != "" {
				atomic.AddInt32(&hits, 1)
			}
		}()
	}
	wg.Wait()
	if hits != 50 {
		t.Errorf("all renders should return cached value; got %d/50", hits)
	}
	// Counter file must still say 1 — script never re-ran.
	out := strings.TrimSpace(r.Render(context.Background(), Input{}))
	if out != "1" {
		t.Errorf("script re-ran under concurrency: got %q", out)
	}
}

// Transient script failures must not blank the bar — the previous
// good value should keep displaying until a fresh successful run
// replaces it.
func TestRenderKeepsLastGoodValueAcrossFailures(t *testing.T) {
	// First config returns success; we swap to a failing config
	// after the cache is warm.
	good := New(Config{Command: `echo good`, Refresh: 1 * time.Millisecond})
	out := good.Render(context.Background(), Input{})
	if out != "good" {
		t.Fatalf("warmup: got %q", out)
	}
	// Force the cache to expire, but make the underlying command
	// fail. The runner must keep emitting "good".
	good.cfg.Command = "exit 1"
	time.Sleep(5 * time.Millisecond)
	out2 := good.Render(context.Background(), Input{})
	if out2 != "good" {
		t.Errorf("failing refresh should keep last-good; got %q", out2)
	}
}

// JSON shape sanity — make sure the contract documented in the
// package comment matches what the script actually receives.
func TestInputMarshalsExpectedFields(t *testing.T) {
	in := Input{
		Model: "claude-sonnet-4-6", Cwd: "/tmp/p",
		Mode: "plan", Turns: 7, CostUSD: 0.0421, InputTokens: 12345,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"model":"claude-sonnet-4-6"`,
		`"cwd":"/tmp/p"`,
		`"mode":"plan"`,
		`"turns":7`,
		`"cost_usd":0.0421`,
		`"input_tokens":12345`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON missing %q; got %s", want, raw)
		}
	}
}
