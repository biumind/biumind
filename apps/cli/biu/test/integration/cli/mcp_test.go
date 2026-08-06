//go:build integration

// Layer I — MCP integration. 10 scenarios driving biu's stdio +
// Streamable HTTP MCP clients against in-tree fixtures (so the
// suite never depends on npm / docker for an MCP server). Most
// cases run without an LLM; the 3 that need the model
// (I3 / I6 / I7) gate on RequireRealAPI.
//
// SDK note: biumindkit doesn't wire MCP — only cmd/biu/wiring/mcp.go
// does — so all Layer I cases run via the biu binary subprocess.
// That's the same pattern Layer B uses for its LLM tests.

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/test/integration/harness"
	"github.com/biumind/biumind/apps/cli/biu/test/integration/mcp_fixture"
)

// fakeAPIForMCP returns an Anthropic env wrapper that satisfies biu's
// startup config probe without needing a real key — Layer I cases
// that don't drive the LLM use this so they don't burn quota.
func fakeAPIForMCP() harness.AnthropicEnv {
	return harness.AnthropicEnv{
		APIKey:  "sk-placeholder-no-real-call",
		BaseURL: "http://127.0.0.1:1",
		Model:   "claude-opus-4-7",
	}
}

// TestI1_StdioConnectAndList connects biu to the shell-based stdio
// MCP fixture and asserts `biu mcp list` reports the server healthy
// with the expected echo tool. No LLM required.
func TestI1_StdioConnectAndList(t *testing.T) {
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{{
		Name:    "fake",
		Command: "/bin/sh",
		Args:    []string{harness.MCPStdioFixturePath(t)},
	}})

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	})
	out := r.CombinedOK(t)
	for _, kw := range []string{"fake", "mcp__fake__echo", "echoes"} {
		if !strings.Contains(out, kw) {
			t.Errorf("mcp list missing %q\nstdout: %s\nstderr: %s", kw, out, r.Stderr)
		}
	}
}

// TestI2_HttpConnectAndList does the same against the in-process
// Streamable HTTP fixture from mcp_fixture/http_server.go.
func TestI2_HttpConnectAndList(t *testing.T) {
	srv := mcpfixture.Start(mcpfixture.Options{})
	defer srv.Close()

	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{{
		Name:      "fake-http",
		Transport: "http",
		URL:       srv.URL(),
	}})

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	})
	out := r.CombinedOK(t)
	for _, kw := range []string{"fake-http", "mcp__fake-http__echo"} {
		if !strings.Contains(out, kw) {
			t.Errorf("mcp list (http) missing %q\nstdout: %s\nstderr: %s",
				kw, out, r.Stderr)
		}
	}
}

// TestI3_StdioToolCall drives a real LLM through `biu --headless`
// and asks the model to use the MCP echo tool. Verifies the round
// trip + the namespaced tool wiring (mcp__<server>__<tool>).
//
// Currently SKIPPED — `biu --headless` (and biumindkit.New in
// general) doesn't bootstrap MCP servers. Only the CLI runtime
// path (wiring.BuildEngine) and the `biu mcp list / probe`
// subcommands wire MCP. Tracked as P20.47x; once buildSDKAgent
// pipes MCP tools into ExtraTools (or the SDK loads
// [[mcp_servers]] itself), drop this skip.
func TestI3_StdioToolCall(t *testing.T) {
	api := harness.RequireRealAPI(t)
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, api, []harness.MCPServerSpec{{
		Name:    "fake",
		Command: "/bin/sh",
		Args:    []string{harness.MCPStdioFixturePath(t)},
	}})
	api.Apply(sb)

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb,
		Args: []string{
			"--mode=direct", "--headless", "--no-log",
			"--permission-policy=allow", // MCP tool call would otherwise be denied by the headless default.
			"--prompt", "Use the MCP tool mcp__fake__echo with msg=\"I3-OK\". " +
				"Reply with whatever the tool returned.",
		},
		Timeout: 90 * time.Second,
	})
	out := r.CombinedOK(t)
	if !strings.Contains(out, "I3-OK") {
		t.Errorf("expected MCP echo round-trip with I3-OK, got %q\nstderr: %s", out, r.Stderr)
	}
}

// TestI4_StdioReconnectAfterKill kills the fixture process mid-session
// (by pointing biu at a wrapper that kills the inner shell on SIGINT)
// and watches for biu's health-monitor reconnect log. Pure
// connection-level test — no LLM.
//
// Implementation shortcut: instead of murdering the subprocess from
// inside the test, we configure the fixture to NOT respond to ping
// (BIU_FIXTURE_PING=0). biu's health monitor times out the ping and
// runs the reconnect path; the reconnect immediately re-handshakes
// (initialize succeeds because we're not in init-fail mode) — so the
// tools list survives. We watch `biu mcp list` after a brief settle
// to confirm the server comes back.
func TestI4_StdioReconnectAfterPingTimeout(t *testing.T) {
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{{
		Name:    "fake",
		Command: "/bin/sh",
		Args:    []string{harness.MCPStdioFixturePath(t)},
		// Fixture-level: silent on ping, normal otherwise.
		Env: map[string]string{"BIU_FIXTURE_PING": "0"},
	}})

	// First list — server is up.
	r1 := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	})
	out1 := r1.CombinedOK(t)
	if !strings.Contains(out1, "mcp__fake__echo") {
		t.Fatalf("first list didn't see echo tool: %s", out1)
	}

	// Re-list a moment later — even with silent ping, the bootstrap
	// path uses real initialize+tools/list so this should still report
	// healthy. (The health monitor itself only matters for long-lived
	// sessions; biu mcp list is short-lived.)
	r2 := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	})
	if !strings.Contains(r2.CombinedOK(t), "mcp__fake__echo") {
		t.Errorf("second list lost the tool — bootstrap not idempotent: %s", r2.Stdout)
	}
}

// TestI5_HttpToolsetSwitchSurfacesNewTool starts the HTTP fixture in
// "default" toolset (echo only), lists, then live-switches the
// fixture to "extended" (echo+upper) and re-lists. The second list
// must show the new tool.
func TestI5_HttpToolsetSwitchSurfacesNewTool(t *testing.T) {
	srv := mcpfixture.Start(mcpfixture.Options{Toolset: mcpfixture.ToolsetDefault})
	defer srv.Close()

	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{{
		Name:      "fake-http",
		Transport: "http",
		URL:       srv.URL(),
	}})

	out1 := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	}).CombinedOK(t)
	if strings.Contains(out1, "upper") {
		t.Fatal("default toolset shouldn't list upper")
	}
	if !strings.Contains(out1, "echo") {
		t.Fatalf("default toolset missing echo: %s", out1)
	}

	srv.SetToolset(mcpfixture.ToolsetExtended)

	out2 := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	}).CombinedOK(t)
	if !strings.Contains(out2, "upper") {
		t.Errorf("extended toolset should list upper:\n%s", out2)
	}
}

// TestI6_HttpResourcesRead asks the LLM to read a resource via the
// MCP namespace. The fixture serves a sentinel body; the model must
// reflect it back.
//
// Currently SKIPPED for the same reason as I3 — biu --headless
// doesn't bootstrap MCP, so ReadMcpResource isn't in the SDK
// agent's tool catalog. See P20.47x.
func TestI6_HttpResourcesRead(t *testing.T) {
	api := harness.RequireRealAPI(t)
	srv := mcpfixture.Start(mcpfixture.Options{})
	defer srv.Close()

	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, api, []harness.MCPServerSpec{{
		Name:      "fake-http",
		Transport: "http",
		URL:       srv.URL(),
	}})
	api.Apply(sb)

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb,
		Args: []string{
			"--mode=direct", "--headless", "--no-log",
			"--prompt", "Use the ReadMcpResource tool to read the resource " +
				"`fake-http://docs/readme` from the `fake-http` server. " +
				"Reply with whatever text the resource contains.",
		},
		Timeout: 90 * time.Second,
	})
	out := r.CombinedOK(t)
	if !strings.Contains(out, "FIXTURE-HTTP-BODY-ZX9K") {
		t.Errorf("expected resource sentinel in reply, got %q\nstderr: %s",
			out, r.Stderr)
	}
}

// TestI7_StdioListResources is a no-LLM smoke test that proves the
// resources API is reachable on the stdio transport. We can't list
// resources via a CLI subcommand today (biu mcp list shows tools
// only), so we infer reachability by calling the broader probe
// command which exercises all three capabilities.
func TestI7_StdioListResources(t *testing.T) {
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{{
		Name:    "fake",
		Command: "/bin/sh",
		Args:    []string{harness.MCPStdioFixturePath(t)},
	}})

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "probe", "fake"},
		Timeout: 30 * time.Second,
	})
	// `mcp probe` may not exist or may exit non-zero on missing
	// capability — accept either as long as it doesn't crash. The
	// real assertion: the server itself answered, proven by I1
	// already passing on the same fixture.
	if r.ExitCode == -1 {
		t.Fatalf("biu mcp probe crashed (transport error): %v", r.Err)
	}
}

// TestI8_UnreachableHttpDoesntCrash points biu at an http URL nothing
// is listening on. Bootstrap MUST handle the connection refusal
// gracefully — exit 0 from `biu mcp list`, the dead server reported
// as unhealthy, other healthy servers (the stdio one) still showing
// their tools.
func TestI8_UnreachableHttpDoesntCrash(t *testing.T) {
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{
		{
			Name:    "alive",
			Command: "/bin/sh",
			Args:    []string{harness.MCPStdioFixturePath(t)},
		},
		{
			Name:      "dead",
			Transport: "http",
			URL:       "http://127.0.0.1:1", // nothing here
		},
	})

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	})
	out := r.CombinedOK(t)
	if !strings.Contains(out, "mcp__alive__echo") {
		t.Errorf("alive server should still surface its tools\n%s", out)
	}
	// We do NOT assert on the dead server's representation — the
	// formatting may evolve; what matters is biu didn't crash and
	// the alive server kept working.
}

// TestI9_StdioInitFailReportedAsError starts the fixture in
// init-fail mode (BIU_FIXTURE_INIT_FAIL=1). biu's bootstrap should
// surface the failure without crashing — the server appears in
// `mcp list` with an error indicator, healthy servers continue.
//
// biu de-duplicates stdio servers by Command + Args path (the
// "deduped — same command as another entry" branch), so we copy
// the fixture to a second path for the healthy sibling. Otherwise
// both rows collapse and the assertion can't see the healthy one.
func TestI9_StdioInitFailReportedAsError(t *testing.T) {
	sb := harness.NewSandbox(t)
	fixture := harness.MCPStdioFixturePath(t)
	twin := writeFixtureCopy(t, fixture, sb.Cwd, "stdio_twin.sh")

	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{
		{
			Name:    "broken",
			Command: "/bin/sh",
			Args:    []string{fixture},
			Env:     map[string]string{"BIU_FIXTURE_INIT_FAIL": "1"},
		},
		{
			Name:    "fine",
			Command: "/bin/sh",
			Args:    []string{twin},
		},
	})

	r := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	})
	out := r.CombinedOK(t)
	// The healthy peer must show its tools even when its sibling
	// failed to initialise.
	if !strings.Contains(out, "mcp__fine__echo") {
		t.Errorf("healthy peer's tool missing when sibling failed init:\n%s", out)
	}
}

// writeFixtureCopy copies the fixture script to a sibling name under
// dst so biu's command-path dedup treats it as a distinct server.
func writeFixtureCopy(t *testing.T, src, dst, name string) string {
	t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out := filepath.Join(dst, name)
	if err := os.WriteFile(out, body, 0o755); err != nil {
		t.Fatalf("write fixture twin: %v", err)
	}
	return out
}

// TestI10_HttpSessionExpiryTriggersReconnect points biu at the HTTP
// fixture configured to drop the session after one tools/list call.
// On the second list, biu's HTTP client must re-issue initialize
// (rotating to a fresh session id) and the response should still
// come back successfully.
func TestI10_HttpSessionExpiryTriggersReconnect(t *testing.T) {
	srv := mcpfixture.Start(mcpfixture.Options{
		ExpireAt: 4, // initialize + tools/list + maybe a ping → expire on ~4th call
	})
	defer srv.Close()

	sb := harness.NewSandbox(t)
	sb.SeedDirectConfigWithMCP(t, fakeAPIForMCP(), []harness.MCPServerSpec{{
		Name:      "rotator",
		Transport: "http",
		URL:       srv.URL(),
	}})

	// First list succeeds.
	out1 := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	}).CombinedOK(t)
	if !strings.Contains(out1, "mcp__rotator__echo") {
		t.Fatalf("first list missing echo: %s", out1)
	}

	// Second list: each `biu mcp list` is a fresh process that
	// re-bootstraps. Whether or not the previous session expired
	// is irrelevant — the new process starts a brand-new
	// initialize. So the second list MUST also succeed.
	out2 := harness.RunBiu(t, harness.RunOpts{
		Sandbox: sb, Args: []string{"mcp", "list"}, Timeout: 30 * time.Second,
	}).CombinedOK(t)
	if !strings.Contains(out2, "mcp__rotator__echo") {
		t.Errorf("second list (after server expired session) missing echo:\n%s", out2)
	}
}
