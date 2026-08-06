// LSP tool — code-intelligence ops via a Language Server Protocol
// backend.
//
// The surface exposes 9 operations (goToDefinition, findReferences,
// hover, documentSymbol, workspaceSymbol, goToImplementation,
// prepareCallHierarchy, incomingCalls, outgoingCalls). This Go port
// keeps the full surface but defers transport (stdio JSON-RPC to
// gopls / pyright / tsserver) to a Backend interface — wiring lands
// in W9 alongside the other heavy native tools.
//
// When Backend is nil the tool soft-errors with a clear message so
// the model falls back to Grep + Read instead of getting stuck.

package web

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// LSPRequest is the per-call payload sent to the LSP server.
type LSPRequest struct {
	Operation string `json:"operation"`
	FilePath  string `json:"filePath"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
	// Operations like workspaceSymbol use a free-form Query field.
	Query string `json:"query,omitempty"`
}

// LSPResponse is whatever the backend produced. Pass-through to the
// model — no Go-side parsing.
type LSPResponse struct {
	Result any `json:"result"`
}

// Backend is the contract a real LSP client implements (gopls / multi-
// language router / etc.). Returns the raw result as a JSON-encodable
// value; the tool stringifies it for the model.
type Backend interface {
	LSP(ctx context.Context, req LSPRequest) (any, error)
}

// LSPTool is the engine-facing tool. Wire a Backend at registration
// time (web.Options.LSP).
type LSPTool struct {
	Backend Backend
}

func (LSPTool) Name() string { return "LSP" }

func (LSPTool) Description(_ map[string]any) string {
	return "Run a Language Server Protocol query: goToDefinition, " +
		"findReferences, hover, documentSymbol, workspaceSymbol, " +
		"goToImplementation, prepareCallHierarchy, incomingCalls, " +
		"outgoingCalls. line/character are 1-indexed."
}

func (LSPTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{
					"goToDefinition", "findReferences", "hover",
					"documentSymbol", "workspaceSymbol",
					"goToImplementation",
					"prepareCallHierarchy", "incomingCalls", "outgoingCalls",
				},
			},
			"filePath":  map[string]any{"type": "string"},
			"line":      map[string]any{"type": "integer"},
			"character": map[string]any{"type": "integer"},
			"query":     map[string]any{"type": "string"},
		},
		"required": []string{"operation"},
	}
}

func (LSPTool) IsReadOnly(_ map[string]any) bool        { return true }
func (LSPTool) IsDestructive(_ map[string]any) bool     { return false }
func (LSPTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (LSPTool) InterruptBehavior() string               { return "cancel" }

func (l LSPTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if l.Backend == nil {
		return softErr("LSP",
			"no LSP backend registered — fall back to Grep + Read"), nil
	}
	op, _ := input["operation"].(string)
	if op == "" {
		return softErr("LSP", "operation is required"), nil
	}
	req := LSPRequest{
		Operation: op,
		FilePath:  asString(input["filePath"]),
		Query:     asString(input["query"]),
	}
	if v, ok := input["line"].(float64); ok {
		req.Line = int(v)
	}
	if v, ok := input["character"].(float64); ok {
		req.Character = int(v)
	}
	res, err := l.Backend.LSP(ctx, req)
	if err != nil {
		return softErr("LSP", err.Error()), nil
	}
	buf, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return softErr("LSP", fmt.Sprintf("encode result: %v", err)), nil
	}
	return text(string(buf)), nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
