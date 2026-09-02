package chat

// retrieval_guard.go — P2 #19 retrieval budget for the agent loop.
//
// Reference: reference/llm_wiki agent/runtime.rs:2822-2851 tiers retrieval
// steps independently of the overall turn cap (2~8 by mode × retrieval
// mode), rejects duplicate retrieval signatures, and force-wraps when
// retrieval stops yielding new information. This is the biumind
// function-calling counterpart, implemented as a per-run guard inside
// AgentLoop (NOT the reference's ReAct protocol):
//
//  1. Step budget — retrieval-class tool calls (tools.Tool.Retrieval)
//     are counted per run; once the budget is spent, further retrieval
//     calls are rejected and the model is told to wrap up.
//  2. Signature dedup — tool name + normalized arguments (trimmed,
//     lower-cased, key-sorted JSON); a repeat is rejected outright:
//     "you already retrieved this — answer from what you have".
//  3. No-yield early stop — N consecutive empty retrieval results
//     ({"results": []}) reject further retrieval with a wrap-up hint.
//
// Rejections are surfaced through BlockEmitter.ToolStarted + ToolFailed,
// so they show up as visible tool steps in the client (not silent), and
// are fed back to the model as a tool_result error — the loop then
// continues so the model can write its final answer (same recovery
// pattern as ordinary tool errors).
//
// The guard is active only when the caller sets AgentLoop.RetrievalBudget
// > 0 (wiki agent run wires mode tiers fast=2/standard=4/deep=6, mirroring
// the MaxTurns 4/8/12 ratio). A zero budget leaves the loop untouched —
// plain chat keeps its existing behaviour.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// retrievalGuard is the per-run mutable state. Not goroutine-safe — the
// agent loop is strictly sequential.
type retrievalGuard struct {
	budget   int // max retrieval invocations per run
	noYield  int // consecutive empty results before early stop
	used     int // retrieval invocations executed so far
	emptyRun int // current consecutive-empty streak
	seen     map[string]struct{}
}

func newRetrievalGuard(budget, noYield int) *retrievalGuard {
	if noYield <= 0 {
		noYield = 3
	}
	return &retrievalGuard{
		budget:  budget,
		noYield: noYield,
		seen:    map[string]struct{}{},
	}
}

// check decides whether a retrieval call may proceed. A non-empty string
// means REJECTED, and the string is the message fed back to the model
// (and shown in the client's tool step). Order: duplicate → budget →
// no-yield, so the most specific explanation wins.
func (g *retrievalGuard) check(name string, input json.RawMessage) string {
	sig := retrievalSignature(name, input)
	if _, dup := g.seen[sig]; dup {
		return fmt.Sprintf(
			"retrieval guard: duplicate call to %q with the same arguments — you already retrieved this; answer from the results you have instead of repeating the search",
			name)
	}
	if g.used >= g.budget {
		return fmt.Sprintf(
			"retrieval guard: retrieval budget exhausted (%d/%d calls used) — stop searching and write your final answer from the information already gathered",
			g.used, g.budget)
	}
	if g.emptyRun >= g.noYield {
		return fmt.Sprintf(
			"retrieval guard: the last %d retrieval calls returned no new information — stop searching and write your final answer from what you have",
			g.emptyRun)
	}
	return ""
}

// record bookkeeping after a retrieval call actually executed (check
// passed). result is the invoker's return value; err non-nil means the
// tool failed — counted against the budget (the model spent a step) but
// neutral for the empty streak (a failure is not "no new information").
func (g *retrievalGuard) record(name string, input json.RawMessage, result any, err error) {
	g.seen[retrievalSignature(name, input)] = struct{}{}
	g.used++
	if err != nil {
		return
	}
	if isEmptyRetrievalResult(result) {
		g.emptyRun++
	} else {
		g.emptyRun = 0
	}
}

// retrievalSignature normalizes tool name + arguments so semantically
// identical calls compare equal: JSON map keys are emitted sorted by
// encoding/json; string values are trimmed and lower-cased (recursively)
// so "Foo" / " foo " / "FOO" collapse to one signature.
func retrievalSignature(name string, input json.RawMessage) string {
	var v any
	if err := json.Unmarshal(input, &v); err != nil {
		// Unparseable input: fall back to the raw bytes — still dedups
		// byte-identical repeats.
		return name + "\x00" + string(input)
	}
	b, err := json.Marshal(normalizeJSONValue(v))
	if err != nil {
		return name + "\x00" + string(input)
	}
	return name + "\x00" + string(b)
}

func normalizeJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeJSONValue(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = normalizeJSONValue(val)
		}
		return t
	case string:
		return strings.ToLower(strings.TrimSpace(t))
	default:
		return v
	}
}

// isEmptyRetrievalResult reports whether a retrieval tool returned zero
// hits. All builtin retrieval tools (websearch / wiki_search /
// memory_recall) return {"query": ..., "results": [...]}, so an empty
// "results" array is the no-yield signal. Unknown shapes → false
// (conservative: never early-stop on a result we don't understand).
func isEmptyRetrievalResult(result any) bool {
	m, ok := result.(map[string]any)
	if !ok {
		return false
	}
	switch r := m["results"].(type) {
	case []any:
		return len(r) == 0
	case []map[string]any:
		return len(r) == 0
	default:
		return false
	}
}
