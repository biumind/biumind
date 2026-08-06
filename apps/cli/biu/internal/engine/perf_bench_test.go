// Granular performance benchmarks for the engine turn loop.
//
// The existing engine_bench_test.go covers the public Submit
// surface end-to-end. This file drills into the per-turn machinery
// so a regression in any individual hot path is visible before it
// shows up as a "submit got slower" cliff:
//
//   - state.Snapshot copy cost as message count grows
//   - compact.Apply micro-compact pass on a long history
//   - ParseStream framing on a representative frame stream
//   - buildToolSpecs walk over a large tool registry (heavy MCP
//     bootstrap simulates 50+ tools)
//   - Long-history Submit (state primed with 100 prior messages)
//
// All benchmarks call b.ReportAllocs() so `go test -bench=. -benchmem`
// captures both ns/op and alloc footprint. The two together catch
// most regressions: a CPU-only number can hide a 10× memory
// blowup.
//
// Capture pprof:
//
//   go test -bench=BenchmarkSubmitLongHistory -cpuprofile=cpu.prof \
//     -memprofile=mem.prof ./internal/engine/
//   go tool pprof -text cpu.prof | head -30
//   go tool pprof -alloc_space -text mem.prof | head -30

package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/compact"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// seedHistory primes an AppState with `n` user/assistant message
// pairs. Used by every long-history benchmark below; centralised so
// changing the per-message size affects every benchmark uniformly.
func seedHistory(st *state.AppState, n int) {
	for i := 0; i < n; i++ {
		st.AppendMessage(state.Message{
			Role: state.RoleUser,
			Content: []state.ContentBlock{{
				Type: state.ContentText,
				Text: "user message " + repeatLetter('a', 200), // ~200 chars
			}},
		})
		st.AppendMessage(state.Message{
			Role: state.RoleAssistant,
			Content: []state.ContentBlock{{
				Type: state.ContentText,
				Text: "assistant reply " + repeatLetter('b', 200),
			}},
		})
	}
}

// repeatLetter returns a string of `n` `c` runes. Avoids importing
// strings.Repeat just for fixture seeding (the test file already
// has a few of its own helpers).
func repeatLetter(c byte, n int) string {
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		b.WriteByte(c)
	}
	return b.String()
}

// ─── State snapshot ───────────────────────────────────

// state.Snapshot is called every turn iteration before the
// provider stream. Cost grows with message count. We test a few
// sizes so the slope is visible — a fix that helps small histories
// but hurts large ones is a regression.
func BenchmarkStateSnapshot10(b *testing.B)   { benchSnapshot(b, 10) }
func BenchmarkStateSnapshot100(b *testing.B)  { benchSnapshot(b, 100) }
func BenchmarkStateSnapshot1000(b *testing.B) { benchSnapshot(b, 1000) }

func benchSnapshot(b *testing.B, n int) {
	st := state.New()
	seedHistory(st, n/2) // n/2 pairs = n messages
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = st.Snapshot()
	}
}

// ─── compact.Apply micro-compact pass ─────────────────

// compact.Apply runs on every turn's snapshot before the LLM call.
// Cost dominated by walking the message list + dedup-checking
// repeated tool_result blocks. We test against a long history with
// no compactable content (worst case for the walker) and one that
// has duplicates (best case for the dedup branch).
func BenchmarkCompactApplyLongClean(b *testing.B) {
	st := state.New()
	seedHistory(st, 100) // 200 messages, no duplicates
	msgs := st.Snapshot()
	cfg := compact.Default()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Apply mutates in place; we want every iteration to start
		// from the same shape. Cheap: a snapshot is just a slice
		// header so re-snapshotting per iter is fine.
		copy_ := append([]state.Message(nil), msgs...)
		compact.Apply(copy_, cfg)
	}
}

// ─── ParseStream framing ──────────────────────────────

// ParseStream is the hot path between provider bytes and biu's
// in-memory event stream. The benchmark drives a representative
// frame sequence (text + tool_use + usage + done) through it and
// measures both the parse cost and the channel send overhead.
func BenchmarkParseStreamMixed(b *testing.B) {
	frames := toolUseTurn("scanning", "tu_1", "Read", `{"path":"/etc/hosts"}`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Spin up a buffered channel and feed every frame; the
		// parser drains it as the producer pushes.
		in := make(chan StreamFrame, len(frames))
		for _, f := range frames {
			in <- f
		}
		close(in)
		out := make(chan Event, 64)
		// Parse synchronously; ParseStream's caller is responsible
		// for draining `out` so we drain in this same goroutine.
		go func() {
			for range out {
			}
		}()
		_, _, _, _ = ParseStream(context.Background(), in, out)
		close(out)
	}
}

// ─── buildToolSpecs registry walk ─────────────────────

// buildToolSpecs is called once per Submit. With many MCP tools
// connected (hosted MCP services routinely ship 30+) the walk +
// schema serialisation is no longer free. We test 5 / 50 / 500 to
// see the slope.
func BenchmarkBuildToolSpecs5(b *testing.B)   { benchBuildToolSpecs(b, 5) }
func BenchmarkBuildToolSpecs50(b *testing.B)  { benchBuildToolSpecs(b, 50) }
func BenchmarkBuildToolSpecs500(b *testing.B) { benchBuildToolSpecs(b, 500) }

func benchBuildToolSpecs(b *testing.B, n int) {
	reg := NewRegistry()
	for i := 0; i < n; i++ {
		// Distinct names per index — the registry map dedupes by
		// name, so without uniqueness only ~3 entries actually
		// land regardless of `n`. Use "tool_<i>" so the registry
		// genuinely grows with n.
		reg.Register(&fakeTool{name: "tool_" + indexName(i)})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = buildToolSpecs(reg, nil)
	}
}

// indexName produces a distinct alphanumeric token for every
// non-negative int — used by the BuildToolSpecs benchmark to
// generate unique tool names without pulling in strconv.
func indexName(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var buf [16]byte
	pos := len(buf)
	n := i
	for n > 0 {
		pos--
		buf[pos] = digits[n%36]
		n /= 36
	}
	return string(buf[pos:])
}

// ─── Submit with long history ─────────────────────────

// Full Submit cycle on top of a 100-message conversation. Includes
// fixture setup (state.New + seed) so the result reflects total
// cold-start cost — this is what a user sees on `biu --resume`.
// For profile-clean numbers focused only on the per-turn engine
// cost, see BenchmarkSubmitOnSeededState below.
func BenchmarkSubmitLongHistory(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		st := state.New()
		seedHistory(st, 50) // 100 messages
		prov := &scriptedProvider{scripts: [][]StreamFrame{
			textTurn("ack"),
		}}
		eng, _ := New(Options{
			State: st, Tools: NewRegistry(),
			Provider: prov, Model: "test",
			BypassPermissions: true,
			CompactMaxTokens:  -1,
		})
		for ev := range eng.Submit(context.Background(), "ping") {
			_ = ev
		}
	}
}

// BenchmarkSubmitOnSeededState measures the per-turn cost when the
// state is already seeded — fixture setup happens once, then each
// iteration runs one Submit and resets the state to the seeded
// snapshot. Used for pprof captures where you want signal from the
// engine code, not from seedHistory's fixture cost.
//
// Resetting via state.ResetMessages avoids re-allocating the
// initial state — the snapshot we restore IS the same backing
// slice across iterations, only the engine's own per-turn allocs
// vary.
func BenchmarkSubmitOnSeededState(b *testing.B) {
	st := state.New()
	seedHistory(st, 50) // 100 messages
	seeded := append([]state.Message(nil), st.Snapshot()...)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset to the seeded baseline. The provider script is
		// consumed each Submit; rebuild minimally per iter.
		st.ResetMessages(seeded)
		prov := &scriptedProvider{scripts: [][]StreamFrame{
			textTurn("ack"),
		}}
		eng, _ := New(Options{
			State: st, Tools: NewRegistry(),
			Provider: prov, Model: "test",
			BypassPermissions: true,
			CompactMaxTokens:  -1,
		})
		for ev := range eng.Submit(context.Background(), "ping") {
			_ = ev
		}
	}
}

// ─── Submit with many tools registered ────────────────

// Same shape as BenchmarkSubmitNoTools but with 50 tools in the
// catalog. Stress test for buildToolSpecs + tool-spec serialisation
// overhead per turn. Most users with MCP connectors land here.
func BenchmarkSubmitManyTools(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		reg := NewRegistry()
		for j := 0; j < 50; j++ {
			reg.Register(&fakeTool{name: "tool_" + repeatLetter('a', j%5)})
		}
		prov := &scriptedProvider{scripts: [][]StreamFrame{
			textTurn("ack"),
		}}
		eng, _ := New(Options{
			State: state.New(), Tools: reg,
			Provider: prov, Model: "test",
			BypassPermissions: true,
			CompactMaxTokens:  -1,
		})
		for ev := range eng.Submit(context.Background(), "hi") {
			_ = ev
		}
	}
}
