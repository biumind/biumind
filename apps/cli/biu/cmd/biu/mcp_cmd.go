// `biu mcp` — diagnostic subcommand for inspecting configured MCP
// servers + the tool catalog they expose.
//
//   biu mcp list           list all servers and their tools
//   biu mcp probe <name>   re-launch one server and dump tools/list
//
// Useful when a server is misbehaving and you want to know whether
// the issue is the spawn step, the handshake, or a specific tool
// returning an unexpected schema.

package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/cmd/biu/wiring"
	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd(f *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Inspect Model Context Protocol servers + tools",
	}
	cmd.AddCommand(newMCPListCmd(f), newMCPProbeCmd(f))
	return cmd
}

func newMCPListCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers and the tools they expose",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, path, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}
			if len(cfg.MCPServers) == 0 {
				if path == "" {
					path = "(no config file found)"
				}
				fmt.Println("no [[mcp_servers]] configured in " + path)
				fmt.Println("see ~/.biu/config.toml example in BiuMind-Code design doc")
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			reg := mcp.NewRegistry()
			defer reg.Close()

			inputs := make([]mcp.BootstrapInput, 0, len(cfg.MCPServers))
			disabledNames := map[string]bool{}
			for _, s := range cfg.MCPServers {
				if s.Disabled {
					fmt.Printf("⊘ %-16s (disabled)\n", s.Name)
					disabledNames[s.Name] = true
					continue
				}
				// Use the shared mapper from wiring/mcp.go so HTTP
				// transport (URL + Headers) reaches Bootstrap. The old
				// inline construction silently dropped Transport / URL
				// / Headers — every HTTP server then hit the stdio
				// Start path with empty command and reported the
				// confusing "exec: no command".
				inputs = append(inputs, wiring.MCPInputFromConfig("manual", s))
			}
			results := reg.Bootstrap(ctx, inputs, func(line string) {
				fmt.Fprintln(os.Stderr, " ", line)
			})
			for _, r := range results {
				switch {
				case r.Skipped:
					fmt.Printf("⊘ %-16s (deduped — same command as another entry)\n", r.Name)
				case r.Err != nil:
					fmt.Printf("✗ %-16s %v\n", r.Name, r.Err)
				default:
					fmt.Printf("✓ %s\n", r.Name)
				}
				for _, m := range r.Missing {
					fmt.Printf("    ⚠ env var ${%s} unset (no default)\n", m)
				}
			}
			fmt.Println()
			tools := reg.All()
			sort.Slice(tools, func(i, j int) bool {
				return tools[i].QualifiedName < tools[j].QualifiedName
			})
			for _, t := range tools {
				fmt.Printf("  • %s\n", t.QualifiedName)
				if t.Def.Description != "" {
					fmt.Printf("    %s\n", truncate(t.Def.Description, 100))
				}
			}
			return nil
		},
	}
}

func newMCPProbeCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "probe <server-name>",
		Short: "Launch one server, print its handshake + tool catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}
			var found *config.MCPServerSection
			for i := range cfg.MCPServers {
				if cfg.MCPServers[i].Name == args[0] {
					found = &cfg.MCPServers[i]
					break
				}
			}
			if found == nil {
				return clierr.WithHint(
					clierr.Newf("mcp probe", "no server named %q in config", args[0]),
					"run `biu mcp list` to see registered servers, or add the [[mcp_servers]] block to ~/.biu/config.toml")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			// Per-transport client construction. The pre-fix path
			// hard-coded NewStdio + StdioConfig — every HTTP server
			// hit Start() with empty command and surfaced the
			// confusing "exec: no command" error.
			var c mcp.Client
			switch mcp.Transport(found.Transport) {
			case mcp.TransportHTTP:
				c = mcp.NewHTTP(mcp.HTTPConfig{
					Name:    found.Name,
					URL:     found.URL,
					Headers: found.Headers,
				})
			default: // stdio (and empty Transport for legacy configs)
				c = mcp.NewStdio(mcp.StdioConfig{
					Name:    found.Name,
					Command: found.Command,
					Args:    found.Args,
					Env:     found.Env,
					Cwd:     found.Cwd,
					StderrSink: func(line string) {
						fmt.Fprintln(os.Stderr, "[stderr]", line)
					},
				})
			}
			if err := c.Start(ctx); err != nil {
				return err
			}
			defer c.Close()

			info, err := c.Initialize(ctx)
			if err != nil {
				return clierr.Wrapf("mcp probe", err, "initialize handshake")
			}
			fmt.Printf("server: %s v%s\n", info.ServerInfo.Name, info.ServerInfo.Version)
			fmt.Printf("protocol: %s\n", info.ProtocolVersion)
			if info.Instructions != "" {
				fmt.Printf("instructions: %s\n", info.Instructions)
			}
			fmt.Println()

			tools, err := c.ListTools(ctx)
			if err != nil {
				return clierr.Wrapf("mcp probe", err, "tools/list")
			}
			fmt.Printf("%d tools:\n", len(tools))
			for _, t := range tools {
				fmt.Printf("  %s\n", t.Name)
				if t.Description != "" {
					fmt.Printf("    %s\n", truncate(t.Description, 100))
				}
			}
			return nil
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
