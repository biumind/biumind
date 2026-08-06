// Engine tools that expose the MCP `prompts/*` surface to the
// model: ListMcpPrompts (browse) and GetMcpPrompt (render).
//
// MCP prompts are server-defined message templates — typical use
// cases are "summarise the changelog", "draft a release note", or
// other domain-specific composition tasks the server author has
// curated. Without these wrappers the model would only know about
// MCP tools (RPC actions) and miss the full protocol.
//
// Tooling shape follows ListMcpResources / ReadMcpResource exactly
// so callers familiar with one pair learn the other immediately.
// Both tools auto-register when the registry has at least one
// connected server (see RegisterEngineTools).

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// listMcpPromptsTool implements ListMcpPrompts.
type listMcpPromptsTool struct {
	reg *Registry
}

func (listMcpPromptsTool) Name() string { return "ListMcpPrompts" }

func (listMcpPromptsTool) Description(_ map[string]any) string {
	return "List prompt templates advertised by connected MCP servers. " +
		"Each entry includes the prompt name, description, and any " +
		"arguments the server expects. Pass an optional `server` filter " +
		"to scope the list to one server. Use GetMcpPrompt to actually " +
		"render a prompt."
}

func (listMcpPromptsTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server": map[string]any{
				"type":        "string",
				"description": "Optional server name. Empty = list prompts from every connected server.",
			},
		},
	}
}

func (listMcpPromptsTool) IsReadOnly(_ map[string]any) bool        { return true }
func (listMcpPromptsTool) IsDestructive(_ map[string]any) bool     { return false }
func (listMcpPromptsTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (listMcpPromptsTool) InterruptBehavior() string               { return "cancel" }

func (l listMcpPromptsTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if l.reg == nil {
		return softErr("ListMcpPrompts", "MCP registry not wired in this build"), nil
	}
	server, _ := input["server"].(string)
	server = strings.TrimSpace(server)
	entries, errs := l.reg.ListPrompts(ctx, server)

	var b strings.Builder
	if server != "" {
		fmt.Fprintf(&b, "server: %s\n", server)
	}
	fmt.Fprintf(&b, "prompts: %d\n", len(entries))
	if len(entries) == 0 && len(errs) == 0 {
		b.WriteString("(none — connected servers don't expose any prompts)\n")
	}
	for _, p := range entries {
		fmt.Fprintf(&b, "  [%s] %s", p.Server, p.Name)
		if p.Description != "" {
			fmt.Fprintf(&b, "  — %s", p.Description)
		}
		b.WriteByte('\n')
		// One line per argument so the model knows what to pass into
		// GetMcpPrompt without re-running ListMcpPrompts.
		for _, a := range p.Arguments {
			required := ""
			if a.Required {
				required = " (required)"
			}
			fmt.Fprintf(&b, "       arg %s%s", a.Name, required)
			if a.Description != "" {
				fmt.Fprintf(&b, " — %s", a.Description)
			}
			b.WriteByte('\n')
		}
	}
	if len(errs) > 0 {
		b.WriteString("\nerrors:\n")
		for _, e := range errs {
			fmt.Fprintf(&b, "  %s\n", e.Error())
		}
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: b.String()}},
	}, nil
}

// getMcpPromptTool implements GetMcpPrompt.
type getMcpPromptTool struct {
	reg *Registry
}

func (getMcpPromptTool) Name() string { return "GetMcpPrompt" }

func (getMcpPromptTool) Description(_ map[string]any) string {
	return "Render a prompt template from a connected MCP server. " +
		"Pass `name` (returned by ListMcpPrompts) plus optional `arguments` " +
		"(an object of string→string). Without `server`, biu tries every " +
		"connected server and returns the first successful render. The " +
		"result contains the prompt's description plus an ordered " +
		"sequence of role/content messages you can fold into your reply."
}

func (getMcpPromptTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Prompt template name (returned by ListMcpPrompts).",
			},
			"server": map[string]any{
				"type":        "string",
				"description": "Optional server name. Empty = scan every connected server.",
			},
			"arguments": map[string]any{
				"type":                 "object",
				"description":          "Optional template arguments — string keys, string values.",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []string{"name"},
	}
}

func (getMcpPromptTool) IsReadOnly(_ map[string]any) bool        { return true }
func (getMcpPromptTool) IsDestructive(_ map[string]any) bool     { return false }
func (getMcpPromptTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (getMcpPromptTool) InterruptBehavior() string               { return "cancel" }

func (g getMcpPromptTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if g.reg == nil {
		return softErr("GetMcpPrompt", "MCP registry not wired in this build"), nil
	}
	name, _ := input["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return softErr("GetMcpPrompt", "name is required"), nil
	}
	server, _ := input["server"].(string)
	server = strings.TrimSpace(server)

	// Coerce arguments map to string→string. JSON objects deserialise
	// into map[string]any in Go; we stringify each value defensively.
	var args map[string]string
	if raw, ok := input["arguments"].(map[string]any); ok && len(raw) > 0 {
		args = make(map[string]string, len(raw))
		for k, v := range raw {
			args[k] = fmt.Sprintf("%v", v)
		}
	}

	res, fromServer, err := g.reg.GetPrompt(ctx, server, name, args)
	if err != nil {
		return softErr("GetMcpPrompt", err.Error()), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "server: %s\nprompt: %s\nmessages: %d\n",
		fromServer, name, len(res.Messages))
	if res.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", res.Description)
	}
	for i, m := range res.Messages {
		fmt.Fprintf(&b, "\n--- message %d (%s)", i+1, m.Role)
		if m.Content.MimeType != "" {
			fmt.Fprintf(&b, " %s", m.Content.MimeType)
		}
		b.WriteString(" ---\n")
		switch m.Content.Type {
		case "text":
			b.WriteString(m.Content.Text)
			if !strings.HasSuffix(m.Content.Text, "\n") {
				b.WriteByte('\n')
			}
		case "image":
			fmt.Fprintf(&b, "[image: %s, %d base64 chars]\n", m.Content.MimeType, len(m.Content.Data))
		case "audio":
			fmt.Fprintf(&b, "[audio: %s, %d base64 chars]\n", m.Content.MimeType, len(m.Content.Data))
		case "resource":
			b.WriteString("[resource reference]\n")
		default:
			fmt.Fprintf(&b, "[unsupported content type %q]\n", m.Content.Type)
		}
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: b.String()}},
	}, nil
}
