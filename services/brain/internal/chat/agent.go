package chat

import (
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// AgentLoop is the chat-mode agent kernel handle. The implementation is
// RunV2 (agent_v2.go) — biumindkit drives the multi-turn LLM
// conversation with tool-call round-trips; this struct only carries the
// shared configuration (tool registry, budgets, whitelist).
//
// 设计文档: docs/BiuMind-Chat-Optimization-Design.md §4.6 (cloud
// agent loop) and §3.2 (ChunkType protocol).
//
// When the registry has zero cloud-runtime tools the kernel degenerates
// to a single LLM turn. So flipping the agent on costs nothing for
// threads that haven't opted into tools yet.
type AgentLoop struct {
	Registry *tools.Registry
	// MaxTurns is the default tool-loop cap for callers that don't set
	// AgentRunInputV2.MaxTurns explicitly (the SSE chat path wires this
	// through; 0 → biumindkit default 25). RunSingleTurn leaves it at
	// the biumindkit default.
	MaxTurns int

	// RetrievalBudget (P2 #19) caps retrieval-class tool calls
	// (tools.Tool.Retrieval) per run, independently of MaxTurns. 0 →
	// no retrieval budget (default; plain chat keeps prior behaviour).
	// RunV2 folds the guard into the biumindkit tool Invokers
	// (retrievalGuard.WrapTool). The wiki agent run wires mode tiers
	// fast=2/standard=4/deep=6 — see wiki/api wikiAgentRetrievalBudget;
	// the agentplane WS chat runner wires the standard tier (4) at
	// cmd/brain/main.go. When active, the loop also rejects duplicate
	// retrieval signatures and early-stops after NoYieldStreakLimit
	// consecutive empty results (retrieval_guard.go).
	RetrievalBudget int
	// NoYieldStreakLimit is the consecutive-empty-results threshold for
	// early stop. 0 → 3 (only meaningful when RetrievalBudget > 0).
	NoYieldStreakLimit int

	// ChatToolAllowlist is the chat-mode tool whitelist (Q1, Runtime v3
	// §4). When non-nil it default-denies: RunV2 advertises only tools
	// whose name is in the set. nil = no restriction
	// (kernel-mechanics tests). Production sets it to
	// tools.DefaultChatToolAllowlist — this is a chat kernel, so the gate
	// always applies in prod. See tools/chatmode.go.
	ChatToolAllowlist map[string]struct{}
}

// NewAgentLoop wires defaults. Registry may be nil — that disables
// tool-use entirely while still streaming text via the same code path.
func NewAgentLoop(reg *tools.Registry) *AgentLoop {
	return &AgentLoop{Registry: reg, MaxTurns: 8}
}

// AgentRunResult summarises the run for persistence.
type AgentRunResult struct {
	StopReason       string
	PromptTokens     int
	CompletionTokens int
}
