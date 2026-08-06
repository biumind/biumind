package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// stubRegistry mirrors enough of Registry's surface for the adapter
// to drive end-to-end without spawning a real subprocess. We can't
// just construct *Registry here because Call requires a connected
// client; instead we register a fake tool entry then exercise the
// adapter's Call path through a small swap.
func TestRegisterEngineToolsBindsNamesAndSchema(t *testing.T) {
	r := NewRegistry()
	r.tools["mcp__demo__hello"] = &RegisteredTool{
		QualifiedName: "mcp__demo__hello",
		Server:        "demo",
		OriginalName:  "hello",
		Def: ToolDef{
			Name:        "hello",
			Description: "say hello",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"to"},
				"properties": map[string]any{
					"to": map[string]any{"type": "string"},
				},
			},
		},
	}
	engReg := engine.NewRegistry()
	names := r.RegisterEngineTools(engReg)
	if len(names) != 1 || names[0] != "mcp__demo__hello" {
		t.Errorf("names=%v", names)
	}
	got, ok := engReg.Get("mcp__demo__hello")
	if !ok {
		t.Fatal("adapter not registered")
	}
	if !strings.Contains(got.Description(nil), "say hello") {
		t.Errorf("description not forwarded: %q", got.Description(nil))
	}
	schema := got.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type = %v", schema["type"])
	}
	// Default safety flags: ask before running.
	if got.IsReadOnly(nil) || got.IsDestructive(nil) || got.IsConcurrencySafe(nil) {
		t.Errorf("MCP tool defaults wrong: ro=%v dest=%v safe=%v",
			got.IsReadOnly(nil), got.IsDestructive(nil), got.IsConcurrencySafe(nil))
	}
}

func TestEngineAdapterCallSurfacesUnknownAsSoft(t *testing.T) {
	r := NewRegistry()
	r.tools["mcp__missing__x"] = &RegisteredTool{
		QualifiedName: "mcp__missing__x", Server: "missing",
	}
	engReg := engine.NewRegistry()
	r.RegisterEngineTools(engReg)
	tool, _ := engReg.Get("mcp__missing__x")
	out, err := tool.Call(context.Background(), map[string]any{}, nil)
	if err != nil {
		t.Fatal(err) // adapter must not raise hard error
	}
	if !out.IsError || !strings.Contains(out.SoftError, "server") {
		t.Errorf("missing server should soft-error; got %+v", out)
	}
}
