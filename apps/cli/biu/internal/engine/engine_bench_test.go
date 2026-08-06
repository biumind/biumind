package engine

import (
	"context"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// BenchmarkSubmitNoTools is the lower-bound for a single turn: scripted
// provider, no tool calls, no permission flow. Anything above this
// number is overhead biu added on top of the bare LLM RTT.
func BenchmarkSubmitNoTools(b *testing.B) {
	for i := 0; i < b.N; i++ {
		// One scripted reply per iteration — fresh provider so we
		// don't run out of scripts.
		prov := &scriptedProvider{scripts: [][]StreamFrame{
			textTurn("ack"),
		}}
		eng, _ := New(Options{
			State: state.New(), Tools: NewRegistry(),
			Provider: prov, Model: "test",
			BypassPermissions: true,
			CompactMaxTokens:  -1,
		})
		for ev := range eng.Submit(context.Background(), "hi") {
			_ = ev
		}
	}
}

// BenchmarkSubmitParallelTools exercises the batcher's
// concurrency-safe parallel path. Two no-op tools per turn.
func BenchmarkSubmitParallelTools(b *testing.B) {
	for i := 0; i < b.N; i++ {
		prov := &scriptedProvider{scripts: [][]StreamFrame{
			twoToolsTurn(
				"u1", "Read", `{"path":"/a"}`,
				"u2", "Glob", `{"pattern":"*.go"}`),
			textTurn("done"),
		}}
		reg := NewRegistry()
		reg.Register(&fakeTool{name: "Read", readOnly: true, concurrencySafe: true})
		reg.Register(&fakeTool{name: "Glob", readOnly: true, concurrencySafe: true})
		eng, _ := New(Options{
			State: state.New(), Tools: reg, Provider: prov, Model: "test",
			BypassPermissions: true, CompactMaxTokens: -1,
		})
		for ev := range eng.Submit(context.Background(), "scan") {
			_ = ev
		}
	}
}
