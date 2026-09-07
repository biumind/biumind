package tools

import "context"

// runIDKey is the unexported context key for the wiki agent run id.
// Write tools that snapshot page_revisions pull it via RunIDFromContext so
// pre-write snapshots can be attributed to the run that caused them
// (BiuMind-Agent-Experience-Design §1.2 P2 变更审计).
type runIDKey struct{}

// WithRunID returns a child context tagged with the agent run id.
// handleWikiAgentRun injects it next to WithUserID; the run id is
// client-generated (POST .../agent/run body) so the audit panel can
// correlate. Empty runID → no-op (manual edits / MCP paths carry none).
//
// 与 WithUserID 同理：run 归属是调用链元数据，不该编进 LLM 可见的工具
// input，走 context.Value 透传。
func WithRunID(ctx context.Context, runID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunIDFromContext returns the run id stashed by WithRunID, or "" if
// absent. "" 是合法值（人工编辑 / 无 run 上下文），调用方据此写 NULL。
func RunIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(runIDKey{}).(string)
	return v
}
