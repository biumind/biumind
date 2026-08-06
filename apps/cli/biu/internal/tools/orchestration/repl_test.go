package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// stubTool is a minimal engine.Tool a REPL test can dispatch into
// without spinning up real Read / Bash / etc. Records its inputs
// + returns a canned result.
type stubTool struct {
	name        string
	calls       int
	lastInput   map[string]any
	wantErr     bool
	resultBody  string
}

func (s *stubTool) Name() string                       { return s.name }
func (s *stubTool) Description(map[string]any) string  { return "stub: " + s.name }
func (s *stubTool) InputSchema() map[string]any        { return map[string]any{"type": "object"} }
func (s *stubTool) IsReadOnly(map[string]any) bool     { return true }
func (s *stubTool) IsDestructive(map[string]any) bool  { return false }
func (s *stubTool) IsConcurrencySafe(map[string]any) bool { return true }
func (s *stubTool) InterruptBehavior() string          { return "complete" }
func (s *stubTool) Call(ctx context.Context, in map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	s.calls++
	s.lastInput = in
	if s.wantErr {
		return &engine.ToolResultPayload{
			IsError:   true,
			SoftError: "stub error from " + s.name,
			Content:   []state.ContentBlock{{Type: state.ContentText, Text: "soft fail"}},
		}, nil
	}
	body := s.resultBody
	if body == "" {
		body = "ok from " + s.name
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: body}},
	}, nil
}

// regWith builds a simple registry containing the given stub tools.
func regWith(tools ...engine.Tool) *engine.SimpleRegistry {
	r := engine.NewRegistry()
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

// ─── env gating ──────────────────────────────────────────────

func TestIsREPLModeEnabled(t *testing.T) {
	t.Setenv("BIU_REPL", "")
	t.Setenv("BIU_REPL_MODE", "")
	if IsREPLModeEnabled() {
		t.Error("default should be off")
	}
	t.Setenv("BIU_REPL", "1")
	if !IsREPLModeEnabled() {
		t.Error("BIU_REPL=1 should enable")
	}
	t.Setenv("BIU_REPL", "0")
	if IsREPLModeEnabled() {
		t.Error("BIU_REPL=0 should disable even when MODE is set later")
	}
	t.Setenv("BIU_REPL", "")
	t.Setenv("BIU_REPL_MODE", "true")
	if !IsREPLModeEnabled() {
		t.Error("BIU_REPL_MODE=true should enable")
	}
}

func TestREPLPrimitiveTools_includesExpected(t *testing.T) {
	got := REPLPrimitiveTools()
	for _, want := range []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash", "Agent"} {
		if !got[want] {
			t.Errorf("REPLPrimitiveTools missing %q", want)
		}
	}
}

// ─── batch dispatch ──────────────────────────────────────────

func TestREPLTool_singleCall(t *testing.T) {
	read := &stubTool{name: "Read", resultBody: "file contents"}
	tool := REPLTool{Registry: regWith(read)}

	res, err := tool.Call(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{
				"tool":  "Read",
				"input": map[string]any{"path": "x.go"},
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("expected success, got %+v", res)
	}
	if read.calls != 1 {
		t.Errorf("Read should fire once, got %d", read.calls)
	}
	if read.lastInput["path"] != "x.go" {
		t.Errorf("input not forwarded: %v", read.lastInput)
	}
	if !strings.Contains(textOfPayload(res), "file contents") {
		t.Errorf("body missing: %s", textOfPayload(res))
	}
}

func TestREPLTool_multiCallSequencing(t *testing.T) {
	read := &stubTool{name: "Read", resultBody: "A"}
	edit := &stubTool{name: "Edit", resultBody: "B"}
	bash := &stubTool{name: "Bash", resultBody: "C"}
	tool := REPLTool{Registry: regWith(read, edit, bash)}

	res, _ := tool.Call(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "Read", "input": map[string]any{}},
			map[string]any{"tool": "Edit", "input": map[string]any{}},
			map[string]any{"tool": "Bash", "input": map[string]any{}},
		},
	}, nil)
	if res.IsError {
		t.Fatal("batch should succeed")
	}
	body := textOfPayload(res)
	for _, want := range []string{"3/3 calls executed", "[0] Read", "[1] Edit", "[2] Bash", "A", "B", "C"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
}

func TestREPLTool_unknownToolDoesntAbortBatch(t *testing.T) {
	read := &stubTool{name: "Read"}
	tool := REPLTool{Registry: regWith(read)}

	res, _ := tool.Call(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "Read", "input": map[string]any{}},
			map[string]any{"tool": "Nonexistent", "input": map[string]any{}},
			map[string]any{"tool": "Read", "input": map[string]any{}},
		},
	}, nil)
	body := textOfPayload(res)
	if !strings.Contains(body, "ERROR: unknown tool") {
		t.Errorf("missing unknown-tool error: %s", body)
	}
	if read.calls != 2 {
		t.Errorf("Read should still run twice, got %d", read.calls)
	}
}

func TestREPLTool_stopOnError(t *testing.T) {
	read := &stubTool{name: "Read"}
	tool := REPLTool{Registry: regWith(read)}

	res, _ := tool.Call(context.Background(), map[string]any{
		"stop_on_error": true,
		"calls": []any{
			map[string]any{"tool": "Nonexistent", "input": map[string]any{}},
			map[string]any{"tool": "Read", "input": map[string]any{}},
		},
	}, nil)
	if read.calls != 0 {
		t.Errorf("stop_on_error should prevent Read; got %d calls", read.calls)
	}
	body := textOfPayload(res)
	if !strings.Contains(body, "1/2 calls executed") {
		t.Errorf("expected 1/2 (aborted), got: %s", body)
	}
}

func TestREPLTool_softErrorReportedNotPanic(t *testing.T) {
	bad := &stubTool{name: "Bash", wantErr: true}
	tool := REPLTool{Registry: regWith(bad)}

	res, _ := tool.Call(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "Bash", "input": map[string]any{}},
		},
	}, nil)
	body := textOfPayload(res)
	if !strings.Contains(body, "tool reported soft error") {
		t.Errorf("soft error should be surfaced: %s", body)
	}
}

func TestREPLTool_batchTooLarge(t *testing.T) {
	tool := REPLTool{Registry: regWith()}
	calls := make([]any, MaxBatchSize+1)
	for i := range calls {
		calls[i] = map[string]any{"tool": "Read", "input": map[string]any{}}
	}
	res, _ := tool.Call(context.Background(), map[string]any{"calls": calls}, nil)
	if !res.IsError {
		t.Errorf("oversize batch should soft-error")
	}
	if !strings.Contains(textOfPayload(res), "too large") {
		t.Errorf("error message should mention size: %s", textOfPayload(res))
	}
}

func TestREPLTool_emptyBatch(t *testing.T) {
	tool := REPLTool{Registry: regWith()}
	res, _ := tool.Call(context.Background(), map[string]any{"calls": []any{}}, nil)
	if !res.IsError {
		t.Errorf("empty batch should error")
	}
}

func TestREPLTool_nilRegistry(t *testing.T) {
	res, _ := REPLTool{}.Call(context.Background(), map[string]any{
		"calls": []any{map[string]any{"tool": "X", "input": map[string]any{}}},
	}, nil)
	if !res.IsError {
		t.Errorf("nil registry should soft-error")
	}
}

// ─── destructiveness inheritance ─────────────────────────────

func TestREPLTool_isDestructiveWhenWriteInBatch(t *testing.T) {
	tool := REPLTool{}
	in := map[string]any{
		"calls": []any{
			map[string]any{"tool": "Read", "input": map[string]any{}},
			map[string]any{"tool": "Write", "input": map[string]any{}},
		},
	}
	if !tool.IsDestructive(in) {
		t.Error("batch with Write should be destructive")
	}
}

func TestREPLTool_isReadOnlyWhenAllReads(t *testing.T) {
	tool := REPLTool{}
	in := map[string]any{
		"calls": []any{
			map[string]any{"tool": "Read", "input": map[string]any{}},
			map[string]any{"tool": "Glob", "input": map[string]any{}},
			map[string]any{"tool": "Grep", "input": map[string]any{}},
		},
	}
	if tool.IsDestructive(in) {
		t.Error("read-only batch should not be destructive")
	}
}

// ─── helpers ────────────────────────────────────────────────

func textOfPayload(p *engine.ToolResultPayload) string {
	if p == nil {
		return ""
	}
	if p.SoftError != "" {
		return p.SoftError
	}
	var b strings.Builder
	for _, c := range p.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}
