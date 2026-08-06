// Engine adapter: bridges mcp.Registry tools onto engine.Tool so the
// agent loop can call MCP servers natively (vs the legacy
// string-returning tools.Tool indirection).
//
// Tool names follow the `mcp__<server>__<tool>` shape — the form the
// permission rules and tool catalog already use, so MCP tools integrate
// without a separate naming scheme.
//
// Concurrency / destructiveness: MCP servers don't tell us whether
// their tools are read-only, so we conservatively flag every MCP
// tool as non-read-only + non-destructive. The runner then prompts
// for permission on first use unless rules whitelist them.

package mcp

import (
	"context"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// engineAdapter promotes one RegisteredTool onto the engine.Tool
// interface. The Registry is shared across adapters so adding /
// removing servers at runtime stays cheap.
type engineAdapter struct {
	reg  *Registry
	tool *RegisteredTool
}

func (a *engineAdapter) Name() string { return a.tool.QualifiedName }

func (a *engineAdapter) Description(_ map[string]any) string {
	if a.tool.Def.Description != "" {
		return a.tool.Def.Description
	}
	return fmt.Sprintf("MCP tool from server %q", a.tool.Server)
}

func (a *engineAdapter) InputSchema() map[string]any {
	if a.tool.Def.InputSchema != nil {
		return a.tool.Def.InputSchema
	}
	return map[string]any{
		"type": "object", "properties": map[string]any{},
	}
}

// We don't have authoritative MCP metadata for these flags. Default
// to safe-but-asks: not read-only, not destructive, not concurrency-
// safe. Operators can override per-tool via permission rules
// (`mcp__server(...)`).
func (a *engineAdapter) IsReadOnly(_ map[string]any) bool        { return false }
func (a *engineAdapter) IsDestructive(_ map[string]any) bool     { return false }
func (a *engineAdapter) IsConcurrencySafe(_ map[string]any) bool { return false }
func (a *engineAdapter) InterruptBehavior() string               { return "cancel" }

// ShouldDefer (P20.51 Phase 2) returns true when the parent MCP
// server's BootstrapInput set DeferTools = true. Consulted by
// engine.PartitionDeferred at every turn — the registry's deferred
// flag is the source of truth, so toggling it at runtime (e.g. via
// a future `/mcp defer <name>` slash) takes effect immediately.
func (a *engineAdapter) ShouldDefer() bool {
	if a == nil || a.reg == nil {
		return false
	}
	return a.reg.IsServerDeferred(a.tool.Server)
}

func (a *engineAdapter) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	res, err := a.reg.Call(ctx, a.tool.QualifiedName, input)
	if err != nil {
		return softErr(a.tool.QualifiedName, err.Error()), nil
	}
	// MCP returns a content-block array (text/image/resource_link).
	// We map text + resource into ContentText so the LLM receives
	// something usable; non-text blocks become a brief description.
	blocks := make([]state.ContentBlock, 0, len(res.Content))
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			blocks = append(blocks, state.ContentBlock{
				Type: state.ContentText, Text: c.Text,
			})
		default:
			blocks = append(blocks, state.ContentBlock{
				Type: state.ContentText,
				Text: fmt.Sprintf("[mcp %s block; %d bytes]",
					c.Type, len(c.Text)),
			})
		}
	}
	if len(blocks) == 0 {
		blocks = []state.ContentBlock{{Type: state.ContentText, Text: ""}}
	}
	return &engine.ToolResultPayload{
		Content: blocks, IsError: res.IsError,
	}, nil
}

// RegisterEngineTools attaches every currently-registered MCP tool to
// the supplied engine registry. Also registers the protocol-level
// resource tools (ListMcpResources / ReadMcpResource) when at least
// one server is connected — those tools query the registry directly
// rather than wrapping a per-server tool. Returns the qualified
// names so callers can log or use them in completion UIs. Safe to
// call multiple times — last-write-wins on the engine side.
func (r *Registry) RegisterEngineTools(reg *engine.SimpleRegistry) []string {
	if reg == nil || r == nil {
		return nil
	}
	all := r.All()
	names := make([]string, 0, len(all)+2)
	for _, t := range all {
		reg.Register(&engineAdapter{reg: r, tool: t})
		names = append(names, t.QualifiedName)
	}
	// Resource tools are gated on having any connected server — when
	// the registry is empty there's nothing for them to talk to and
	// shipping the schema would just nag the model.
	r.mu.RLock()
	hasServers := len(r.clients) > 0
	// Snapshot needs-auth HTTP clients while we hold the lock so the
	// pseudo-tool registration loop below doesn't have to keep it.
	type authStub struct {
		name   string
		client *HTTPClient
	}
	var stubs []authStub
	for n, c := range r.clients {
		if hc, ok := c.(*HTTPClient); ok && hc.NeedsAuth() {
			stubs = append(stubs, authStub{name: n, client: hc})
		}
	}
	r.mu.RUnlock()
	if hasServers {
		reg.Register(listMcpResourcesTool{reg: r})
		reg.Register(readMcpResourceTool{reg: r})
		reg.Register(listMcpPromptsTool{reg: r})
		reg.Register(getMcpPromptTool{reg: r})
		names = append(names,
			"ListMcpResources", "ReadMcpResource",
			"ListMcpPrompts", "GetMcpPrompt")
	}
	// Auth pseudo-tools (P20.49): one per HTTPClient currently in
	// needs-auth state, surfaced under `mcp__<server>__authenticate`.
	// The model uses them to discover the OAuth challenge + drive the
	// PKCE flow once that ships in P20.49b.
	for _, s := range stubs {
		t := authPseudoTool{serverName: s.name, client: s.client}
		reg.Register(t)
		names = append(names, t.Name())
	}
	return names
}

func softErr(name, msg string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: fmt.Sprintf("%s error: %s", name, msg),
		}},
		IsError: true, SoftError: msg,
	}
}
