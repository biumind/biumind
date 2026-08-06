# engine benchmarks

Run from `apps/cli/biu/`:

```bash
# Full suite, 2s per case (~40s wall clock)
go test -bench=. -benchmem -run=^$ -benchtime=2s ./internal/engine/

# Single benchmark with pprof capture
go test -bench=BenchmarkSubmitOnSeededState -benchmem -run=^$ \
  -benchtime=2s -cpuprofile=cpu.prof -memprofile=mem.prof \
  ./internal/engine/

# Inspect profiles
go tool pprof -top -cum cpu.prof
go tool pprof -top -alloc_space mem.prof
```

## Baseline (Apple M4, darwin/arm64, 2026-05-26)

Numbers from a clean run with no other load. ns/op may swing ±10%
across runs; allocations are deterministic.

| Benchmark                         |     ns/op |     B/op | allocs |
|-----------------------------------|----------:|---------:|-------:|
| SubmitNoTools                     |     4,581 |    5,395 |     45 |
| SubmitParallelTools               |    15,957 |   14,106 |    133 |
| SubmitOnSeededState (100 msgs)    |     5,979 |   22,705 |     36 |
| SubmitLongHistory (cold + seed)   |    84,981 |  137,522 |    551 |
| SubmitManyTools (50 tools)        |     8,130 |   11,656 |    193 |
| StateSnapshot10                   |       152 |    1,792 |      1 |
| StateSnapshot100                  |     1,211 |   18,432 |      1 |
| StateSnapshot1000                 |    18,016 |  172,032 |      1 |
| CompactApplyLongClean (200 msgs)  |     3,753 |   40,960 |      1 |
| ParseStreamMixed                  |     2,537 |    3,400 |     24 |
| BuildToolSpecs5                   |       672 |    2,048 |     17 |
| BuildToolSpecs50                  |     6,091 |   20,544 |    152 |
| BuildToolSpecs500                 |    50,705 |  204,672 |  1,502 |

## What the numbers mean

**Per-turn ceiling (no LLM):** ~5-6μs for a vanilla turn on either
empty state or 100-msg history. The user-facing turn is dominated by
LLM RTT (hundreds of ms); biu's per-turn overhead is essentially
free relative to the network call.

**Cold start cost:** The "seed + Submit" combination on a 100-msg
history is ~85μs. That's `biu --resume` reloading a long
conversation. Still dwarfed by the first LLM call.

**Hot paths in per-turn allocation** (from `pprof -alloc_space` on
SubmitOnSeededState):

  1. `state.AppState.Snapshot` — **~81%** of allocations. Each
     turn's snapshot copies the message slice so that
     `compact.Apply` can mutate it without touching the parent
     state. Inherent to the current design — see "potential
     optimisations" below.
  2. `QueryEngine.Submit` channel allocation — ~6%
  3. `scriptedProvider.Stream` (test fixture) — ~2%
  4. `ParseStream` — ~1.5% (string interning would help)
  5. `permissions.NewContext` — ~1.6% (only because the bench
     constructs a fresh engine per iteration; production engines
     are reused)

**Tool-spec catalog scaling** (from BuildToolSpecs5/50/500):

  - Linear in tool count: ~115ns and ~458 bytes per tool.
  - 4 allocs per tool — was 5 before the `Description(nil)` fix
    (see PR/commit landing in P20.40).
  - For a typical user with 5-10 tools, this is below 1μs and 5KB
    of catalog assembly per Submit. Negligible.

## Optimisations applied

### `Description(nil)` instead of `Description(map[string]any{})`

`buildToolSpecs` formerly passed an empty map to every tool's
`Description()` call as a placeholder. Every Description impl
ignores the input (the parameter is `_ map[string]any` everywhere).
The empty map was one heap allocation per tool per Submit — 25% of
buildToolSpecs allocs.

Switched to `nil`. Outcomes (BuildToolSpecs50):

  - Allocations: 202 → 152 (-25%)
  - Bytes: 22,944 → 20,544 (-10%)
  - ns/op: 5,348 → 5,062 (-5%)

The contract change is documented in `helpers.go::buildToolSpecs`;
any future Description impl that wants to inspect input must audit
nil-tolerance.

## Potential optimisations (not applied)

These showed up in the profile but the data didn't justify a fix —
either the cost is dominated by LLM RTT in production, or the
optimisation would risk correctness. Documented so future work has
context.

### Avoid `state.Snapshot` copy when possible

81% of per-Submit allocs come from snapshot copying. Two paths:

  1. **Copy-on-write**: AppState already only `append`s; the
     existing slice's backing array can be safely shared until
     someone mutates it. `compact.Apply` mutates the snapshot in
     place — change it to return a new slice, then `Snapshot`
     could hand out the existing slice without copying. Risk:
     subtle if other callers mutate. Worth doing if profiling on
     real workloads shows GC pressure.

  2. **Pool snapshots**: keep a sync.Pool of `[]Message` slices.
     Engine borrows one per turn, returns it after compact.Apply.
     Lower-friction than CoW. Adds complexity; only worth it if a
     production workload demonstrates allocation pressure.

Both are deferred — production turns are LLM-RTT-bound, so
24KB/turn of GC pressure is invisible against 100KB/s of network
traffic for a few-tool-call session.

### `ParseStream` allocation churn

24 allocs / 3.4KB per parse. Source: each StreamFrame is converted
to a fresh string via `string([]byte)`. Could pool the string
builder or pass byte slices through the event channel. Same
deferral logic — LLM-bound in production.

### Cache `tool.InputSchema()` per tool

Tools currently rebuild their InputSchema map on every catalog
walk. With many tools (`BuildToolSpecs500`: 1502 allocs) this
accumulates. A per-tool cache would drop the bulk. Touching 30+
tool implementations is a wide refactor; the catalog is built
once per Submit, not per turn iteration, so the impact is bounded.

## When to revisit

- Production telemetry shows GC pause time creeping up.
- A user reports `--resume` taking noticeably long on multi-MB
  session logs.
- The tool catalog grows past ~100 tools (heavy MCP setup) and
  buildToolSpecs starts dominating cold-start latency.
