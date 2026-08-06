// Package must provides helpers that enforce architectural invariants.
// E.g., MustEmit fulfills invariant I4 (any mutation has an events row).
package must

import (
	"context"
	"fmt"
	"log/slog"
)

// EventEmitter is implemented by services that have an events table.
type EventEmitter interface {
	EmitEvent(ctx context.Context, scope, eventType string, payload map[string]any) error
}

// Emit calls e.EmitEvent and panics on failure (intentional — service should
// crash rather than silently miss an event).
//
// Use this in mutation paths:
//
//	must.Emit(ctx, repo, "wiki:project:p1", "block.updated", map[string]any{"id":...})
func Emit(ctx context.Context, e EventEmitter, scope, eventType string, payload map[string]any) {
	if err := e.EmitEvent(ctx, scope, eventType, payload); err != nil {
		slog.ErrorContext(ctx, "must.Emit failed", "scope", scope, "type", eventType, "err", err)
		panic(fmt.Sprintf("must.Emit: failed to emit %s/%s: %v", scope, eventType, err))
	}
}
