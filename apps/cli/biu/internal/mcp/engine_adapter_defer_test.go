package mcp

import (
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// TestEngineAdapter_ShouldDefer_OffByDefault — without an explicit
// SetDeferTools call, an MCP-imported tool reports ShouldDefer=false
// (back-compat: existing biu users see no behavioural change).
func TestEngineAdapter_ShouldDefer_OffByDefault(t *testing.T) {
	r := NewRegistry()
	tool := &RegisteredTool{
		QualifiedName: "mcp__example__do",
		Server:        "example",
		OriginalName:  "do",
	}
	a := &engineAdapter{reg: r, tool: tool}
	if a.ShouldDefer() {
		t.Errorf("default should be non-deferred")
	}
	// engine-level helper agrees.
	if engine.IsDeferred(a) {
		t.Errorf("engine.IsDeferred should agree with adapter")
	}
}

// TestEngineAdapter_ShouldDefer_FromRegistryFlag — flipping the
// registry-level flag changes the adapter's report on the next call,
// without rebuilding the adapter or re-registering tools.
func TestEngineAdapter_ShouldDefer_FromRegistryFlag(t *testing.T) {
	r := NewRegistry()
	tool := &RegisteredTool{
		QualifiedName: "mcp__big__x",
		Server:        "big",
		OriginalName:  "x",
	}
	a := &engineAdapter{reg: r, tool: tool}

	r.SetDeferTools("big", true)
	if !a.ShouldDefer() {
		t.Errorf("should defer after SetDeferTools(true)")
	}
	if !engine.IsDeferred(a) {
		t.Errorf("engine.IsDeferred should report deferred")
	}

	r.SetDeferTools("big", false)
	if a.ShouldDefer() {
		t.Errorf("flag flipped off; should no longer defer")
	}
}

// TestEngineAdapter_ShouldDefer_PerServerScope — flag is per-server,
// not registry-wide. Toggling "github" doesn't affect "slack".
func TestEngineAdapter_ShouldDefer_PerServerScope(t *testing.T) {
	r := NewRegistry()
	gh := &engineAdapter{reg: r, tool: &RegisteredTool{
		QualifiedName: "mcp__github__x", Server: "github",
	}}
	sl := &engineAdapter{reg: r, tool: &RegisteredTool{
		QualifiedName: "mcp__slack__x", Server: "slack",
	}}

	r.SetDeferTools("github", true)
	if !gh.ShouldDefer() {
		t.Errorf("github should defer")
	}
	if sl.ShouldDefer() {
		t.Errorf("slack should NOT defer; flag is per-server")
	}
}

// TestEngineAdapter_ShouldDefer_NilSafety — defensive coverage for
// the (impossible-but-cheap-to-test) nil paths.
func TestEngineAdapter_ShouldDefer_NilSafety(t *testing.T) {
	var a *engineAdapter
	if a.ShouldDefer() {
		t.Errorf("nil adapter should not panic and not defer")
	}
	a2 := &engineAdapter{reg: nil, tool: &RegisteredTool{Server: "x"}}
	if a2.ShouldDefer() {
		t.Errorf("nil registry should not panic and not defer")
	}
}
