package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// runOneViaInternal exercises the runOne dispatch path for type=internal,
// not just runInternal directly — guards against a refactor that
// drops the type=internal branch in runOne.
func runOneViaInternal(t *testing.T, handler InternalHandler, payload []byte) Result {
	t.Helper()
	t.Cleanup(ResetInternal)
	RegisterInternal("test:handler", handler)
	entry := Entry{
		Source:  "test",
		Command: Command{Type: "internal", Handler: "test:handler"},
	}
	results := Run(context.Background(), []Entry{entry}, EventPreToolUse,
		map[string]any{"x": "y"})
	if len(results) != 1 {
		t.Fatalf("Run returned %d results, want 1", len(results))
	}
	_ = payload // payload is constructed inside Run from the map; the
	// handler will see the marshalled bytes
	return results[0]
}

func TestInternalHandler_decisionFlowsThrough(t *testing.T) {
	want := Decision{
		Block:             true,
		Reason:            "no",
		AdditionalContext: "ctx",
	}
	r := runOneViaInternal(t, func(ctx context.Context, payload []byte) (Decision, error) {
		return want, nil
	}, nil)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if !r.Decision.Block || r.Decision.Reason != "no" {
		t.Errorf("Decision lost: %+v", r.Decision)
	}
	if r.Stdout == "" {
		t.Error("Stdout should mirror the decision JSON for telemetry")
	}
	// Confirm Stdout is parseable as the same Decision.
	var got Decision
	if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
		t.Errorf("Stdout not valid JSON: %v (%q)", err, r.Stdout)
	}
}

func TestInternalHandler_payloadDelivered(t *testing.T) {
	var seen []byte
	r := runOneViaInternal(t, func(ctx context.Context, payload []byte) (Decision, error) {
		seen = append(seen, payload...)
		return Decision{}, nil
	}, nil)
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	var got map[string]any
	if err := json.Unmarshal(seen, &got); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if got["x"] != "y" {
		t.Errorf("payload = %v, want x=y", got)
	}
}

func TestInternalHandler_isBlockingViaDecision(t *testing.T) {
	r := runOneViaInternal(t, func(ctx context.Context, payload []byte) (Decision, error) {
		return Decision{Block: true, Reason: "stop"}, nil
	}, nil)
	if !r.IsBlocking() {
		t.Error("Decision.Block=true should make IsBlocking true")
	}
}

func TestInternalHandler_errorBecomesWarning(t *testing.T) {
	r := runOneViaInternal(t, func(ctx context.Context, payload []byte) (Decision, error) {
		return Decision{}, errors.New("transient failure")
	}, nil)
	if r.Err == nil {
		t.Error("handler error should populate Result.Err")
	}
	if r.IsBlocking() {
		t.Error("handler error should NOT block (warn-level)")
	}
}

func TestInternalHandler_missingHandlerErrors(t *testing.T) {
	defer ResetInternal()
	entry := Entry{
		Source:  "test",
		Command: Command{Type: "internal", Handler: "nope:not:registered"},
	}
	results := Run(context.Background(), []Entry{entry}, EventPreToolUse, nil)
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("missing handler should produce Err, got %+v", results)
	}
	if results[0].IsBlocking() {
		t.Error("missing handler is a warning, not a block")
	}
}

func TestInternalHandler_emptyHandlerNameRejected(t *testing.T) {
	defer ResetInternal()
	entry := Entry{
		Source:  "test",
		Command: Command{Type: "internal", Handler: ""},
	}
	results := Run(context.Background(), []Entry{entry}, EventPreToolUse, nil)
	if len(results) != 1 || results[0].Err == nil {
		t.Errorf("empty handler should produce Err, got %+v", results)
	}
}

func TestInternalHandler_timeoutRespected(t *testing.T) {
	defer ResetInternal()
	RegisterInternal("slow", func(ctx context.Context, payload []byte) (Decision, error) {
		select {
		case <-ctx.Done():
			return Decision{}, ctx.Err()
		case <-time.After(2 * time.Second):
			return Decision{}, nil
		}
	})
	entry := Entry{
		Source:  "test",
		Command: Command{Type: "internal", Handler: "slow", Timeout: 1}, // 1s
	}
	start := time.Now()
	results := Run(context.Background(), []Entry{entry}, EventPreToolUse, nil)
	elapsed := time.Since(start)
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("timeout not enforced; elapsed=%s", elapsed)
	}
	if results[0].Err == nil {
		t.Error("timeout should populate Err")
	}
}

func TestRegisterInternal_idempotentAndLookup(t *testing.T) {
	defer ResetInternal()
	h := func(ctx context.Context, payload []byte) (Decision, error) {
		return Decision{}, nil
	}
	RegisterInternal("a", h)
	RegisterInternal("a", h) // re-register same identity → no error
	if got := LookupInternal("a"); got == nil {
		t.Error("Lookup miss after register")
	}
	if got := LookupInternal("missing"); got != nil {
		t.Error("Lookup should return nil for unregistered name")
	}
}

func TestRegisterInternal_emptyOrNilSkipped(t *testing.T) {
	defer ResetInternal()
	RegisterInternal("", func(ctx context.Context, payload []byte) (Decision, error) {
		return Decision{}, nil
	})
	RegisterInternal("name", nil)
	if got := LookupInternal(""); got != nil {
		t.Error("empty name should not register")
	}
	if got := LookupInternal("name"); got != nil {
		t.Error("nil handler should not register")
	}
}

// MergeJSON path: a plugin's hooks JSON with type=internal must
// reach the runner without extra translation. End-to-end smoke
// covering the registry → MergeJSON → Run pipeline.
func TestMergeJSON_routesInternalHook(t *testing.T) {
	defer ResetInternal()
	called := false
	RegisterInternal("plugin:hook", func(ctx context.Context, payload []byte) (Decision, error) {
		called = true
		return Decision{AdditionalContext: "from plugin"}, nil
	})

	reg := NewRegistry()
	reg.MergeJSON("plugin:demo", []byte(`{
		"PreToolUse": [{
			"hooks": [{"type":"internal","handler":"plugin:hook"}]
		}]
	}`))

	entries := reg.For(EventPreToolUse, "")
	if len(entries) != 1 {
		t.Fatalf("registered %d entries, want 1", len(entries))
	}
	results := Run(context.Background(), entries, EventPreToolUse, nil)
	if !called {
		t.Error("internal handler not invoked through MergeJSON path")
	}
	if results[0].Decision.AdditionalContext != "from plugin" {
		t.Errorf("decision = %+v", results[0].Decision)
	}
}

// Confirm that the type=command path still works after the runner
// gained the internal branch — guard against accidental regressions.
func TestRunner_commandBranchUnchanged(t *testing.T) {
	defer ResetInternal()
	entry := Entry{
		Source: "test",
		Command: Command{Type: "command", Command: "echo hi"},
	}
	results := Run(context.Background(), []Entry{entry}, EventPreToolUse, nil)
	if len(results) != 1 {
		t.Fatal("results count")
	}
	r := results[0]
	if r.Err != nil {
		t.Errorf("command hook errored: %v (%s)", r.Err, r.Stderr)
	}
	if r.Stdout == "" {
		t.Error("command hook stdout empty")
	}
	_ = fmt.Sprintf // keep "fmt" used if future refactor drops other call sites
}
