//go:build integration

package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// MCPServerSpec describes one [[mcp_servers]] entry to seed into the
// sandbox's config.toml. Either Command (stdio) OR URL (http) must be
// set, not both.
type MCPServerSpec struct {
	Name      string
	Disabled  bool
	Transport string // "stdio" (default) or "http"

	// stdio
	Command string
	Args    []string
	Env     map[string]string

	// http
	URL     string
	Headers map[string]string
}

// SeedDirectConfigWithMCP writes ~/.biu/config.toml inside the sandbox
// with a direct-mode Anthropic provider AND a list of [[mcp_servers]]
// entries. Combines SeedDirectConfig + MCP wiring into one call so
// tests don't have to seed twice.
func (s *Sandbox) SeedDirectConfigWithMCP(t *testing.T, a AnthropicEnv, servers []MCPServerSpec) string {
	t.Helper()
	dir := filepath.Join(s.Home, ".biu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("harness: mkdir .biu: %v", err)
	}
	path := filepath.Join(dir, "config.toml")

	var b strings.Builder
	b.WriteString("[default]\n")
	b.WriteString("mode = \"direct\"\n")
	b.WriteString("provider = \"anthropic\"\n")
	if a.Model != "" {
		fmt.Fprintf(&b, "model = %q\n", a.Model)
	}
	b.WriteString("\n[providers.anthropic]\n")
	fmt.Fprintf(&b, "api_key = %q\n", a.APIKey)
	if a.BaseURL != "" {
		fmt.Fprintf(&b, "endpoint = %q\n", a.BaseURL)
	}

	for _, srv := range servers {
		b.WriteString("\n[[mcp_servers]]\n")
		fmt.Fprintf(&b, "name = %q\n", srv.Name)
		if srv.Disabled {
			b.WriteString("disabled = true\n")
		}
		if srv.Transport != "" {
			fmt.Fprintf(&b, "transport = %q\n", srv.Transport)
		}
		switch srv.Transport {
		case "http":
			fmt.Fprintf(&b, "url = %q\n", srv.URL)
			if len(srv.Headers) > 0 {
				b.WriteString("headers = {")
				first := true
				for k, v := range srv.Headers {
					if !first {
						b.WriteString(", ")
					}
					first = false
					fmt.Fprintf(&b, "%s = %q", k, v)
				}
				b.WriteString("}\n")
			}
		default: // stdio
			fmt.Fprintf(&b, "command = %q\n", srv.Command)
			if len(srv.Args) > 0 {
				b.WriteString("args = [")
				for i, a := range srv.Args {
					if i > 0 {
						b.WriteString(", ")
					}
					fmt.Fprintf(&b, "%q", a)
				}
				b.WriteString("]\n")
			}
			if len(srv.Env) > 0 {
				b.WriteString("env = {")
				first := true
				for k, v := range srv.Env {
					if !first {
						b.WriteString(", ")
					}
					first = false
					fmt.Fprintf(&b, "%s = %q", k, v)
				}
				b.WriteString("}\n")
			}
		}
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("harness: write config.toml: %v", err)
	}
	return path
}

// MCPStdioFixturePath returns the absolute path to the shell-script
// stdio MCP server fixture under test/integration/mcp_fixture/. The
// script is checked into the repo; this helper just resolves it via
// runtime.Caller so tests don't need to know their own location.
func MCPStdioFixturePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("harness: runtime.Caller failed")
	}
	// thisFile = .../apps/cli/biu/test/integration/harness/mcp.go
	// fixture = .../apps/cli/biu/test/integration/mcp_fixture/stdio_server.sh
	abs := filepath.Join(filepath.Dir(thisFile), "..", "mcp_fixture", "stdio_server.sh")
	abs, err := filepath.Abs(abs)
	if err != nil {
		t.Fatalf("harness: abs fixture path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("harness: stdio fixture missing at %s: %v", abs, err)
	}
	return abs
}
