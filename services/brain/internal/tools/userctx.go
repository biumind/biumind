package tools

import (
	"context"

	"github.com/google/uuid"
)

// userIDKey is the unexported context key for the caller's user id.
// Tools that need owner-scoping pull it via UserIDFromContext.
type userIDKey struct{}

// WithUserID returns a child context tagged with the caller's user id.
// The chat HandleSend / tools proxy endpoint inject this before
// dispatching to the agent loop or directly to Registry.Invoke.
//
// 设计: agent loop 不应该把 user 身份编进每个工具的 input —— 那让
// 工具协议失去通用性,且暴露身份给 LLM。改用 context.Value 传输,
// 工具按需读取。
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	if id == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, userIDKey{}, id)
}

// UserIDFromContext returns the user id stashed by WithUserID, or
// uuid.Nil if absent. Tools that require owner-scoping should treat
// uuid.Nil as a hard error rather than silently returning unscoped
// results.
func UserIDFromContext(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(userIDKey{}).(uuid.UUID)
	return v
}
