// engine_adapter.go — bridge between the legacy tools.Tool struct
// (string-returning) and the new internal/engine.Tool interface.
//
// The legacy registry was a value-typed Tool with `Invoke(ctx, args)
// (string, error)`; the engine wants a behavioural interface returning
// typed *ToolResultPayload. Rather than rewriting all six tool
// implementations in one go, we wrap them.
//
// Future work (Phase B): rewrite read/write/edit/grep/glob/bash
// natively against engine.Tool so they can emit per-line streaming
// progress (bash stdout chunks, glob match-as-you-find, etc.). The
// adapter pattern is the bridge during that migration.

package tools

import (
	"context"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// engineToolAdapter promotes a legacy *Tool to engine.Tool. Read-only
// + concurrency-safe propagate from the struct fields. Destructive
// is heuristic: bash + write + edit are destructive when they touch
// disk; for the simpler tools (read/grep/glob) IsDestructive is
// always false.
type engineToolAdapter struct {
	t *Tool
}

func (a *engineToolAdapter) Name() string { return a.t.Name }

func (a *engineToolAdapter) Description(_ map[string]any) string {
	return a.t.Description
}

func (a *engineToolAdapter) InputSchema() map[string]any {
	if a.t.Schema == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return a.t.Schema
}

func (a *engineToolAdapter) IsReadOnly(_ map[string]any) bool {
	return a.t.IsReadOnly
}

func (a *engineToolAdapter) IsDestructive(input map[string]any) bool {
	// Conservative: bash, write, and edit are always destructive.
	// Read-side tools never are.
	switch a.t.Name {
	case "bash", "write", "edit":
		return true
	}
	return false
}

func (a *engineToolAdapter) IsConcurrencySafe(_ map[string]any) bool {
	return a.t.IsConcurrencySafe
}

func (a *engineToolAdapter) InterruptBehavior() string {
	// bash output streams to disk-modifying commands; better let it
	// finish and let the user re-Ctrl-C.
	if a.t.Name == "bash" {
		return "block"
	}
	return ""
}

func (a *engineToolAdapter) Call(
	ctx context.Context,
	args map[string]any,
	env *engine.ToolEnv,
) (*engine.ToolResultPayload, error) {
	out, err := a.t.Invoke(ctx, args)
	if err != nil {
		// Surface as soft error inside the payload: the engine
		// already wraps non-nil errors at runner-level, but we want
		// the LLM to see the message either way.
		return &engine.ToolResultPayload{
			Content: []state.ContentBlock{{
				Type: state.ContentText,
				Text: err.Error(),
			}},
			IsError:   true,
			SoftError: err.Error(),
		}, nil
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText, Text: out,
		}},
	}, nil
}

// EngineRegistry returns an engine.ToolRegistry backed by this
// legacy Registry. Bridge layer — once tools are rewritten natively
// against engine.Tool, this helper goes away.
func (r *Registry) EngineRegistry() engine.ToolRegistry {
	return r.EngineRegistrySimple()
}

// EngineRegistrySimple is the same as EngineRegistry but returns the
// concrete *engine.SimpleRegistry so callers can Register additional
// native tools onto it before handing it to the engine.
func (r *Registry) EngineRegistrySimple() *engine.SimpleRegistry {
	out := engine.NewRegistry()
	for _, t := range r.tools {
		out.Register(&engineToolAdapter{t: t})
	}
	return out
}

// AsEngineTool wraps a single legacy tool. Useful for tests that want
// to register one specific tool against the engine.
func (t *Tool) AsEngineTool() engine.Tool {
	return &engineToolAdapter{t: t}
}
