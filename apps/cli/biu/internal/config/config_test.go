package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Default.Provider != "anthropic" {
		t.Errorf("provider = %q", d.Default.Provider)
	}
	if d.Permissions.Mode != "ask" {
		t.Errorf("mode = %q", d.Permissions.Mode)
	}
}

func TestLoadFromExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	body := `
[default]
provider = "openai"
model = "gpt-4o"

[model-relay]
endpoint = "http://localhost:7001"

[providers.anthropic]
api_key = "sk-ant-test"

[permissions]
mode = "auto_edit"
allowlist = ["bash:ls", "read:**"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p != path {
		t.Errorf("path = %q", p)
	}
	if cfg.Default.Provider != "openai" || cfg.Default.Model != "gpt-4o" {
		t.Errorf("default %+v", cfg.Default)
	}
	if cfg.Relay.Endpoint != "http://localhost:7001" {
		t.Errorf("model-relay endpoint %q", cfg.Relay.Endpoint)
	}
	if cfg.Providers["anthropic"].APIKey != "sk-ant-test" {
		t.Errorf("provider key wrong")
	}
	if cfg.Permissions.Mode != "auto_edit" {
		t.Errorf("mode %q", cfg.Permissions.Mode)
	}
	if len(cfg.Permissions.Allowlist) != 2 {
		t.Errorf("allowlist %v", cfg.Permissions.Allowlist)
	}
}

// HTTP-transport MCP server entries must round-trip through TOML
// with the new url + headers + transport fields populated. Catches
// regressions in the [[mcp_servers]] schema when the HTTP variant
// landed alongside the legacy stdio shape.
func TestLoadMCPServersHTTPTransport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-http.toml")
	body := `
[[mcp_servers]]
name = "atlassian"
transport = "http"
url = "https://mcp.atlassian.com/v1/sse"
headers = { Authorization = "Bearer abc123", X-Region = "us-east-1" }

[[mcp_servers]]
name = "local-fs"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.MCPServers); got != 2 {
		t.Fatalf("expected 2 mcp servers; got %d", got)
	}
	// HTTP entry: stdio fields stay empty, http fields populated.
	atlassian := cfg.MCPServers[0]
	if atlassian.Name != "atlassian" {
		t.Errorf("name: %q", atlassian.Name)
	}
	if atlassian.Transport != "http" {
		t.Errorf("transport: %q", atlassian.Transport)
	}
	if atlassian.URL != "https://mcp.atlassian.com/v1/sse" {
		t.Errorf("url: %q", atlassian.URL)
	}
	if atlassian.Headers["Authorization"] != "Bearer abc123" {
		t.Errorf("auth header lost: %+v", atlassian.Headers)
	}
	if atlassian.Headers["X-Region"] != "us-east-1" {
		t.Errorf("custom header lost: %+v", atlassian.Headers)
	}
	if atlassian.Command != "" {
		t.Errorf("http entry should not carry stdio Command: %q", atlassian.Command)
	}
	// Stdio entry: transport stays "" so the bootstrap dispatcher's
	// default case (stdio) fires.
	fs := cfg.MCPServers[1]
	if fs.Transport != "" {
		t.Errorf("stdio entry should have empty transport: %q", fs.Transport)
	}
	if fs.Command != "npx" || len(fs.Args) != 3 {
		t.Errorf("stdio fields: %+v", fs)
	}
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	cfg, _, err := Load("/nonexistent/path.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.Provider != "anthropic" {
		t.Errorf("expected defaults preserved")
	}
}
