// Public tool authoring surface.
//
// Consumers of this SDK shouldn't have to import internal/engine to
// register a custom tool. This file mirrors the relevant
// engine.Tool / engine.ToolResultPayload shapes onto exported types,
// then bridges them back inside the SDK.
//
// Minimum example:
//
//   echo := biumindkit.NewTool(biumindkit.ToolDef{
//       Name:        "Echo",
//       Description: "Echo back the input as the tool result.",
//       InputSchema: map[string]any{
//           "type": "object",
//           "properties": map[string]any{
//               "msg": map[string]any{"type": "string"},
//           },
//           "required": []string{"msg"},
//       },
//       Run: func(ctx context.Context, args map[string]any) (string, error) {
//           msg, _ := args["msg"].(string)
//           return msg, nil
//       },
//   })
//   ag, _ := biumindkit.New(biumindkit.Options{
//       APIKey: ..., ExtraTools: []biumindkit.Tool{echo},
//   })

package biumindkit

import (
	"context"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// Tool is the public contract a custom tool implements. Mirrors
// engine.Tool but renames the JSON-Schema field for clarity and
// returns string instead of structured ContentBlocks (the SDK wraps
// for you). Concurrency / destructiveness flags default to safe
// values when zero — every flag is opt-in.
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	IsReadOnly() bool
	IsDestructive() bool
	IsConcurrencySafe() bool
	Run(ctx context.Context, args map[string]any) (string, error)
}

// ToolDef is the convenience-builder shape behind NewTool. Lets
// callers register a tool without writing six methods.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any

	// IsReadOnly defaults to false; set to true for grep / lookup
	// tools so the runner can batch them in parallel.
	IsReadOnly bool

	// IsDestructive defaults to false. The runner asks for permission
	// before running destructive tools (unless the active mode skips
	// the prompt).
	IsDestructive bool

	// IsConcurrencySafe defaults to false. Two safe tools called in
	// the same turn run in parallel; otherwise they serialise.
	IsConcurrencySafe bool

	// Run is the actual implementation. Return the textual result
	// the LLM should see; non-nil error becomes a soft tool error.
	Run func(ctx context.Context, args map[string]any) (string, error)
}

// NewTool turns a ToolDef into a Tool ready for ExtraTools.
func NewTool(def ToolDef) Tool { return &defTool{def: def} }

type defTool struct{ def ToolDef }

func (t *defTool) Name() string                { return t.def.Name }
func (t *defTool) Description() string         { return t.def.Description }
func (t *defTool) InputSchema() map[string]any { return t.def.InputSchema }
func (t *defTool) IsReadOnly() bool            { return t.def.IsReadOnly }
func (t *defTool) IsDestructive() bool         { return t.def.IsDestructive }
func (t *defTool) IsConcurrencySafe() bool     { return t.def.IsConcurrencySafe }
func (t *defTool) Run(ctx context.Context, a map[string]any) (string, error) {
	if t.def.Run == nil {
		return "", nil
	}
	return t.def.Run(ctx, a)
}

// engineToolBridge promotes a public Tool onto engine.Tool so the
// agent loop can dispatch it.
type engineToolBridge struct{ inner Tool }

func (b *engineToolBridge) Name() string                        { return b.inner.Name() }
func (b *engineToolBridge) Description(_ map[string]any) string { return b.inner.Description() }
func (b *engineToolBridge) InputSchema() map[string]any         { return b.inner.InputSchema() }
func (b *engineToolBridge) IsReadOnly(_ map[string]any) bool    { return b.inner.IsReadOnly() }
func (b *engineToolBridge) IsDestructive(_ map[string]any) bool { return b.inner.IsDestructive() }
func (b *engineToolBridge) IsConcurrencySafe(_ map[string]any) bool {
	return b.inner.IsConcurrencySafe()
}
func (b *engineToolBridge) InterruptBehavior() string { return "cancel" }
func (b *engineToolBridge) Call(ctx context.Context, args map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	out, err := b.inner.Run(ctx, args)
	if err != nil {
		return &engine.ToolResultPayload{
			Content: []state.ContentBlock{{Type: state.ContentText, Text: err.Error()}},
			IsError: true, SoftError: err.Error(),
		}, nil
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: out}},
	}, nil
}
