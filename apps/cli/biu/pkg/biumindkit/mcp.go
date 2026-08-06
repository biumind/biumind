package biumindkit

// MCP support — public wrapper around internal MCP registry so embedders
// don't import biu's internal/mcp directly.
//
// Brain S4 doesn't need MCP yet (chat mode runs the 4 cloud tools natively).
// Bridge / CLI S2 already wires MCP via cmd/biu/wiring.BootstrapMCP →
// NewMCPRegistry(...) → Options.MCPRegistry.

import (
	"github.com/biumind/biumind/apps/cli/biu/internal/mcp"
)

// MCPRegistry is the public handle for an MCP server registry. Construct
// it by wrapping a wiring.BootstrapMCP result; biumindkit registers each
// connected server's tools with the standard `mcp__<server>__<tool>` name
// prefix when New() runs.
//
// nil is valid — biumindkit-driven sessions without MCP simply omit MCP
// tool catalog entries.
type MCPRegistry struct {
	inner *mcp.Registry
}

// NewMCPRegistry wraps an internal *mcp.Registry. Returns nil if inner
// is nil so callers don't have to nil-check both layers.
func NewMCPRegistry(inner *mcp.Registry) *MCPRegistry {
	if inner == nil {
		return nil
	}
	return &MCPRegistry{inner: inner}
}

// Inner returns the wrapped internal registry. Reserved for biumindkit
// internal use; embedders should not call this.
func (r *MCPRegistry) Inner() *mcp.Registry {
	if r == nil {
		return nil
	}
	return r.inner
}
