// /mcp slash + helpers. Surfaces connected MCP servers + their
// tools. argsPreview lives here too — it's only used by the MCP
// renderers.

package repl

import (
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/mcp"
)

// handleMCP dispatches the /mcp subcommands. Stateless — every call
// resolves against the in-memory MCP registry directly.
//
//	/mcp                — list connected servers + tool count
//	/mcp <server>       — show that server's tools with descriptions
//
// We surface the actual command that was used to spawn each server
// alongside the tool count so a user diagnosing "why isn't my
// github tool firing" can see (a) the server's connected, (b) its
// tools are registered, (c) the qualified names the LLM sees.
func (m model) handleMCP(parts []string) string {
	if m.mcp == nil {
		return "/mcp: no MCP registry wired (configure servers in config.toml " +
			"under [[mcp_servers]] then restart biu)"
	}
	servers := m.mcp.Servers()
	if len(servers) == 0 {
		return "/mcp: no servers connected. Add a [[mcp_servers]] block to ~/.biu/config.toml."
	}
	if len(parts) == 1 {
		return renderMCPServerList(servers)
	}
	target := parts[1]
	for _, s := range servers {
		if s.Name == target {
			return renderMCPServerDetail(s)
		}
	}
	available := make([]string, 0, len(servers))
	for _, s := range servers {
		available = append(available, s.Name)
	}
	return fmt.Sprintf("/mcp: no server %q. Available: %s",
		target, strings.Join(available, ", "))
}

func renderMCPServerList(servers []mcp.ServerStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MCP servers (%d connected):\n", len(servers))
	for _, s := range servers {
		// Transport tag right after name so a row at a glance shows
		// stdio (subprocess) vs http (remote endpoint).
		t := s.Transport
		if t == "" {
			t = "stdio" // pre-Transport-field servers default
		}
		fmt.Fprintf(&b, "  ✓ %-20s [%s]  %d tool(s)  (%s%s)\n",
			s.Name, t, s.ToolCount, s.Command, argsPreview(s.Args))
	}
	b.WriteString("  (drill into a server: /mcp <name>)")
	return b.String()
}

func renderMCPServerDetail(s mcp.ServerStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MCP server: %s\n", s.Name)
	t := s.Transport
	if t == "" {
		t = "stdio"
	}
	fmt.Fprintf(&b, "  transport: %s\n", t)
	if t == "http" {
		fmt.Fprintf(&b, "  url:     %s\n", s.Command)
	} else {
		fmt.Fprintf(&b, "  command: %s%s\n", s.Command, argsPreview(s.Args))
	}
	fmt.Fprintf(&b, "  tools (%d):\n", len(s.Tools))
	if len(s.Tools) == 0 {
		b.WriteString("  (server returned no tools — its tools/list call was empty)")
		return b.String()
	}
	for _, t := range s.Tools {
		desc := t.Description
		if len(desc) > 80 {
			desc = desc[:77] + "…"
		}
		fmt.Fprintf(&b, "    %s\n", t.QualifiedName)
		if t.OriginalName != "" && t.OriginalName != strings.TrimPrefix(t.QualifiedName, "mcp__"+s.Name+"__") {
			fmt.Fprintf(&b, "      (upstream id: %s)\n", t.OriginalName)
		}
		if desc != "" {
			fmt.Fprintf(&b, "      %s\n", desc)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// argsPreview formats an args slice for the inline command preview.
// Shows up to ~40 chars + an ellipsis so a long npx invocation
// doesn't blow the row layout.
func argsPreview(args []string) string {
	if len(args) == 0 {
		return ""
	}
	joined := " " + strings.Join(args, " ")
	if len(joined) > 40 {
		return joined[:37] + "…"
	}
	return joined
}
