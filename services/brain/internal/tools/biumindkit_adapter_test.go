// S4-2 BiumindkitAdapter tests. 验 4 个 builtin tool（time / web / wiki /
// memory.recall）通过 adapter 跑 biumindkit.Tool 接口仍正确。

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAdapter_NilInvokerReturnsNil(t *testing.T) {
	descOnly := Tool{
		Descriptor: Descriptor{Name: "client_only", Description: "x", Runtime: RuntimeClient},
	}
	if got := BiumindkitAdapter(descOnly); got != nil {
		t.Errorf("descriptor-only tool adapted to non-nil: %+v", got)
	}
}

func TestAdapter_InvokerRoundtrip(t *testing.T) {
	tt := Tool{
		Descriptor: Descriptor{
			Name:        "echo",
			Description: "echos arg.x",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
			Runtime:     RuntimeBoth,
		},
		ReadOnly: true, // read-only chat tool → adapter marks IsReadOnly + concurrency-safe
		Invoke: func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				X string `json:"x"`
			}
			_ = json.Unmarshal(raw, &args)
			return map[string]any{"echo": args.X}, nil
		},
	}
	bk := BiumindkitAdapter(tt)
	if bk == nil {
		t.Fatal("adapter nil")
	}
	if bk.Name() != "echo" {
		t.Errorf("name=%q", bk.Name())
	}
	if !bk.IsReadOnly() {
		t.Errorf("expected IsReadOnly=true (read-only tool passthrough)")
	}
	if !bk.IsConcurrencySafe() {
		t.Errorf("expected IsConcurrencySafe=true (read-only tools are concurrency-safe)")
	}
	if bk.IsDestructive() {
		t.Errorf("read-only chat tools should not be destructive")
	}
	out, err := bk.Run(context.Background(), map[string]any{"x": "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, `"echo":"hi"`) {
		t.Errorf("result body lost: %q", out)
	}
}

// TestAdapter_WriteToolProjectedAsMutating (S3 P0-1) — a ReadOnly=false
// tool (wiki write tool) must project through the adapter as IsReadOnly=false
// + IsConcurrencySafe=false, so biumindkit treats it as side-effecting.
func TestAdapter_WriteToolProjectedAsMutating(t *testing.T) {
	tt := Tool{
		Descriptor: Descriptor{
			Name:        "wiki_create_page",
			Description: "create page",
			Runtime:     RuntimeCloud,
		},
		ReadOnly: false,
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"created": true}, nil
		},
	}
	bk := BiumindkitAdapter(tt)
	if bk == nil {
		t.Fatal("adapter nil")
	}
	if bk.IsReadOnly() {
		t.Errorf("write tool should project IsReadOnly=false")
	}
	if bk.IsConcurrencySafe() {
		t.Errorf("write tool should project IsConcurrencySafe=false")
	}
}

func TestAdapter_StringOutputPassthrough(t *testing.T) {
	// Invoke returns string —— adapter 不二次 JSON-encode（不要双引号）
	tt := Tool{
		Descriptor: Descriptor{Name: "raw_text", Runtime: RuntimeCloud},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "plain text", nil
		},
	}
	bk := BiumindkitAdapter(tt)
	got, err := bk.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "plain text" {
		t.Errorf("got %q want %q (string passthrough broken)", got, "plain text")
	}
}

func TestAdapter_InvokerErrorPropagates(t *testing.T) {
	sentinel := errors.New("invoke boom")
	tt := Tool{
		Descriptor: Descriptor{Name: "explode", Runtime: RuntimeCloud},
		Invoke: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, sentinel
		},
	}
	bk := BiumindkitAdapter(tt)
	_, err := bk.Run(context.Background(), nil)
	if !errors.Is(err, sentinel) {
		t.Errorf("err=%v, want %v", err, sentinel)
	}
}

func TestRegistry_AvailableForBiumindkit(t *testing.T) {
	reg := New()
	cloudTool := Tool{
		Descriptor: Descriptor{Name: "c", Runtime: RuntimeCloud},
		Invoke:     func(_ context.Context, _ json.RawMessage) (any, error) { return "ok", nil },
	}
	clientTool := Tool{
		Descriptor: Descriptor{Name: "client_a", Runtime: RuntimeClient},
		Invoke:     func(_ context.Context, _ json.RawMessage) (any, error) { return "n/a", nil },
	}
	bothTool := Tool{
		Descriptor: Descriptor{Name: "b", Runtime: RuntimeBoth},
		Invoke:     func(_ context.Context, _ json.RawMessage) (any, error) { return "ok", nil },
	}
	descOnly := Tool{Descriptor: Descriptor{Name: "d", Runtime: RuntimeCloud}}
	for _, x := range []Tool{cloudTool, clientTool, bothTool, descOnly} {
		if err := reg.Register(x); err != nil {
			t.Fatalf("register %s: %v", x.Name, err)
		}
	}
	got := reg.AvailableForBiumindkit(nil)
	// cloudTool + bothTool 都 Cloud-runtime + 有 invoker → 2 个；
	// clientTool 不 cloud-runtime；descOnly 没 invoker
	if len(got) != 2 {
		names := make([]string, len(got))
		for i, b := range got {
			names[i] = b.Name()
		}
		t.Errorf("AvailableForBiumindkit returned %d tools (%v), want 2", len(got), names)
	}
}
