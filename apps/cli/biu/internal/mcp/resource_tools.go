// Engine tools that expose the MCP `resources/*` surface to the
// model: ListMcpResources (browse) and ReadMcpResource (fetch).
//
// MCP resources are read-only data handles a server publishes —
// files, API snapshots, search-index entries. Tools are how the
// model *acts*; resources are how it *reads*. Without these wrappers
// the model can only invoke MCP tools, missing half the protocol.
//
// We always-register both tools when the registry is non-empty: the
// tool result soft-errors on servers that don't advertise the
// Resources capability, which is friendlier than refusing to load
// the tool at all (some users connect a mix of resource-capable and
// tool-only servers).

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// listMcpResourcesTool implements ListMcpResources.
type listMcpResourcesTool struct {
	reg *Registry
}

func (listMcpResourcesTool) Name() string { return "ListMcpResources" }

func (listMcpResourcesTool) Description(_ map[string]any) string {
	return "List read-only resources advertised by connected MCP servers " +
		"(files, API snapshots, search-index entries). Each entry includes " +
		"a server-qualified URI you can pass to ReadMcpResource. Pass an " +
		"optional `server` filter to scope the list to one server."
}

func (listMcpResourcesTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server": map[string]any{
				"type":        "string",
				"description": "Optional server name. Empty = list resources from every connected server.",
			},
		},
	}
}

func (listMcpResourcesTool) IsReadOnly(_ map[string]any) bool        { return true }
func (listMcpResourcesTool) IsDestructive(_ map[string]any) bool     { return false }
func (listMcpResourcesTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (listMcpResourcesTool) InterruptBehavior() string               { return "cancel" }

func (l listMcpResourcesTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if l.reg == nil {
		return softErr("ListMcpResources", "MCP registry not wired in this build"), nil
	}
	server, _ := input["server"].(string)
	server = strings.TrimSpace(server)
	entries, errs := l.reg.ListResources(ctx, server)

	var b strings.Builder
	if server != "" {
		fmt.Fprintf(&b, "server: %s\n", server)
	}
	fmt.Fprintf(&b, "resources: %d\n", len(entries))
	if len(entries) == 0 && len(errs) == 0 {
		b.WriteString("(none — connected servers don't expose any resources)\n")
	}
	for _, r := range entries {
		// Server-qualified for disambiguation when multiple servers
		// expose overlapping URI namespaces.
		fmt.Fprintf(&b, "  [%s] %s", r.Server, r.URI)
		if r.Name != "" {
			fmt.Fprintf(&b, "  — %s", r.Name)
		}
		if r.MimeType != "" {
			fmt.Fprintf(&b, "  (%s)", r.MimeType)
		}
		b.WriteByte('\n')
		if r.Description != "" {
			fmt.Fprintf(&b, "       %s\n", r.Description)
		}
	}
	if len(errs) > 0 {
		// Partial-failure path: surface every error so the model can
		// see which server is misbehaving without burying the full
		// list. Errors don't taint the IsError flag — empty result
		// with errors is better than no result.
		b.WriteString("\nerrors:\n")
		for _, e := range errs {
			fmt.Fprintf(&b, "  %s\n", e.Error())
		}
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: b.String()}},
	}, nil
}

// readMcpResourceTool implements ReadMcpResource.
type readMcpResourceTool struct {
	reg *Registry
}

func (readMcpResourceTool) Name() string { return "ReadMcpResource" }

func (readMcpResourceTool) Description(_ map[string]any) string {
	return "Fetch the contents of an MCP resource by URI. Pass `uri` " +
		"(returned by ListMcpResources) and optionally `server` to scope " +
		"the lookup to one server. Without a server, biu tries every " +
		"connected server and returns the first hit — useful when the " +
		"URI is globally unique."
}

func (readMcpResourceTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"uri": map[string]any{
				"type":        "string",
				"description": "The resource URI to fetch. Returned in ListMcpResources output.",
			},
			"server": map[string]any{
				"type":        "string",
				"description": "Optional server name. Empty = scan every connected server for the URI.",
			},
		},
		"required": []string{"uri"},
	}
}

func (readMcpResourceTool) IsReadOnly(_ map[string]any) bool        { return true }
func (readMcpResourceTool) IsDestructive(_ map[string]any) bool     { return false }
func (readMcpResourceTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (readMcpResourceTool) InterruptBehavior() string               { return "cancel" }

func (rd readMcpResourceTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if rd.reg == nil {
		return softErr("ReadMcpResource", "MCP registry not wired in this build"), nil
	}
	uri, _ := input["uri"].(string)
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return softErr("ReadMcpResource", "uri is required"), nil
	}
	server, _ := input["server"].(string)
	server = strings.TrimSpace(server)

	res, fromServer, err := rd.reg.ReadResource(ctx, server, uri)
	if err != nil {
		return softErr("ReadMcpResource", err.Error()), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "server: %s\nuri: %s\nparts: %d\n", fromServer, uri, len(res.Contents))
	for i, c := range res.Contents {
		fmt.Fprintf(&b, "\n--- part %d", i+1)
		if c.MimeType != "" {
			fmt.Fprintf(&b, " (%s)", c.MimeType)
		}
		b.WriteString(" ---\n")
		if c.Text != "" {
			b.WriteString(c.Text)
			if !strings.HasSuffix(c.Text, "\n") {
				b.WriteByte('\n')
			}
			continue
		}
		if c.Blob != "" {
			fmt.Fprintf(&b, "[binary blob: %d base64 chars; mime=%s]\n", len(c.Blob), c.MimeType)
			continue
		}
		b.WriteString("(empty content envelope)\n")
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: b.String()}},
	}, nil
}
