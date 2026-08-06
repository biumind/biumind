// MCP bootstrap — connects user-global servers from
// `~/.biu/config.toml` plus project-local servers from
// `<cwd>/.biumind/config.toml`. Project-source entries pass through
// the trust gate so a malicious checkout can't auto-spawn child
// processes.

package wiring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/adapters"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/mcp"
	"github.com/biumind/biumind/apps/cli/biu/internal/trust"
)

// BootstrapMCP connects every server in cfg.MCPServers and (when
// the cwd has a `<cwd>/.biumind/config.toml` file) the project-
// local servers declared there. Returns the populated registry.
// Empty list / connection failures yield nil (logged but non-fatal
// — the agent stays usable without MCP).
//
// Trust gate: project-source entries are filtered out unless cwd
// is on the trusted-directories allow-list. Without this, dropping
// a malicious `.biumind/config.toml` into a repo + opening biu in
// it would auto-spawn whatever child process the file specifies.
// User-source entries (from `~/.biu/config.toml`) bypass the gate
// since the user authored them.
func BootstrapMCP(cfg *config.Config, trustStore *trust.Store) *mcp.Registry {
	inputs := make([]mcp.BootstrapInput, 0, len(cfg.MCPServers))
	// User-global config first (highest trust — user wrote it).
	for _, s := range cfg.MCPServers {
		inputs = append(inputs, mcpInputFromConfig("manual", s))
	}
	// Project-local override layer: <cwd>/.biumind/config.toml's
	// `[[mcp_servers]]` blocks. Source="project" so the gate
	// applies. Errors here are non-fatal (no project file = empty
	// list).
	if extra, err := loadProjectMCPInputs(); err == nil {
		inputs = append(inputs, extra...)
	} else {
		fmt.Fprintf(os.Stderr, "[biu] mcp: project config: %v\n", err)
	}
	if len(inputs) == 0 {
		return nil
	}
	reg := mcp.NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := mcp.BootstrapOptions{
		StderrSink: func(msg string) {
			fmt.Fprintf(os.Stderr, "[biu] mcp: %s\n", msg)
		},
		SkipNotifier: func(name string) {
			fmt.Fprintf(os.Stderr,
				"[biu] mcp/%s: blocked — project-local server in untrusted dir "+
					"(run /trust here to enable)\n", name)
		},
	}
	if trustStore != nil {
		opts.TrustGate = adapters.MCPTrustGate(trustStore)
	}
	results := reg.BootstrapWithOptions(ctx, inputs, opts)
	failed := 0
	healthy := 0
	for _, r := range results {
		switch {
		case r.TrustBlocked:
			// Already logged via SkipNotifier; don't double-count
			// as a failure.
		case r.Err != nil:
			fmt.Fprintf(os.Stderr, "[biu] mcp/%s: %v\n", r.Name, r.Err)
			failed++
		case !r.Skipped:
			healthy++
		}
	}
	if healthy == 0 {
		// Nothing connected — return nil so the engine doesn't ship
		// a registry pointer at an empty server set.
		_ = failed // failed count surfaced via stderr already
		return nil
	}

	// Health monitoring + auto-reconnect. Probes every connected
	// server every 30s; failures arm an exponentially-backed-off
	// reconnect that re-runs the handshake + diffs the new tools
	// catalog. Without this layer a long-running biu session
	// loses connections permanently when the server dies. The
	// monitor's lifetime is tied to the registry — biu's main()
	// path doesn't need to manage it explicitly today, but
	// callers wanting a clean shutdown can pass the returned
	// monitor's Stop into their teardown chain.
	monitor := reg.StartHealthMonitor(mcp.HealthOptions{
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	})
	// Seed each server's catalog so the FIRST reconnect's diff
	// has a meaningful baseline (otherwise it'd report "+N -0"
	// because the previous slice was zero-length).
	if monitor != nil {
		for _, t := range reg.All() {
			// reg.All() flattens to one entry per tool; we want
			// per-server catalogs. Iterate Servers() instead.
			_ = t
			break
		}
		for _, srv := range reg.Servers() {
			tools := make([]mcp.ToolDef, 0, len(srv.Tools))
			for _, t := range srv.Tools {
				tools = append(tools, mcp.ToolDef{
					Name: t.OriginalName, Description: t.Description,
				})
			}
			monitor.SeedCatalog(srv.Name, tools)
		}
	}
	return reg
}

// loadProjectMCPInputs reads `<cwd>/.biumind/config.toml` if present
// and returns its `[[mcp_servers]]` blocks as project-source
// BootstrapInputs. Missing file / unreadable cwd yield (nil, nil) —
// most repos won't ship MCP config and that's fine.
func loadProjectMCPInputs() ([]mcp.BootstrapInput, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	path := filepath.Join(cwd, ".biumind", "config.toml")
	if _, err := os.Stat(path); err != nil {
		// Missing is the common case — not an error, just nothing
		// to add.
		return nil, nil
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	out := make([]mcp.BootstrapInput, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		out = append(out, mcpInputFromConfig("project", s))
	}
	return out, nil
}

// mcpInputFromConfig translates one TOML section into the bootstrap
// shape, picking up either stdio (Command + Args + Env + Cwd) or
// http (URL + Headers) fields based on the Transport selector.
// Empty Transport falls through to stdio so legacy configs keep
// working unchanged.
// MCPInputFromConfig translates one TOML [[mcp_servers]] entry into
// the BootstrapInput shape registry.Bootstrap consumes. Exported so
// `biu mcp list` / `biu mcp probe` use the same routing as the
// runtime — without this, the CLI subcommand silently dropped
// transport / URL / Headers, breaking HTTP servers (P20.47d-I bug).
func MCPInputFromConfig(source string, s config.MCPServerSection) mcp.BootstrapInput {
	return mcpInputFromConfig(source, s)
}

func mcpInputFromConfig(source string, s config.MCPServerSection) mcp.BootstrapInput {
	in := mcp.BootstrapInput{
		Source:     source,
		Name:       s.Name,
		Transport:  mcp.Transport(s.Transport),
		DeferTools: s.DeferTools,
	}
	switch in.Transport {
	case mcp.TransportHTTP:
		in.URL = s.URL
		in.Headers = s.Headers
		// P20.49b: carry the OAuth subtable through so the HTTPClient
		// has what it needs to drive PKCE on 401.
		if s.OAuth != nil {
			in.OAuth = &mcp.OAuthSpec{
				ClientID:     s.OAuth.ClientID,
				AuthorizeURL: s.OAuth.AuthorizeURL,
				TokenURL:     s.OAuth.TokenURL,
				Scopes:       append([]string(nil), s.OAuth.Scopes...),
				CallbackPort: s.OAuth.CallbackPort,
			}
		}
	default:
		// stdio — and the empty case (no transport key in TOML).
		in.Command = s.Command
		in.Args = s.Args
		in.Env = s.Env
		in.Cwd = s.Cwd
	}
	return in
}
