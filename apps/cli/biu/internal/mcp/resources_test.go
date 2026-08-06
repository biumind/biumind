// End-to-end tests for resources/list + resources/read. Drives a
// fake MCP server through the StdioClient and Registry surfaces so
// the wire flow + multi-server fan-out are both covered.

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// resourcefulServer is a tiny MCP server that returns a fixed
// resource catalog and resolves resources/read by URI. Differs from
// fakeServer by also handling the resources methods.
const resourcefulServer = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"docs","version":"0.1"},"capabilities":{"tools":{},"resources":{}}}}'
      ;;
    *'"method":"notifications/initialized"'*)
      ;;
    *'"method":"tools/list"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"tools":[]}}'
      ;;
    *'"method":"resources/list"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"resources":[{"uri":"docs://readme","name":"Readme","mimeType":"text/markdown","description":"project readme"},{"uri":"docs://license","name":"License","mimeType":"text/plain"}]}}'
      ;;
    *'"method":"resources/read"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      uri=$(echo "$line" | sed -n 's/.*"uri":"\([^"]*\)".*/\1/p')
      case "$uri" in
        docs://readme)
          echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"contents":[{"uri":"docs://readme","mimeType":"text/markdown","text":"# Hello"}]}}'
          ;;
        docs://license)
          echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"contents":[{"uri":"docs://license","mimeType":"text/plain","text":"MIT"}]}}'
          ;;
        *)
          echo '{"jsonrpc":"2.0","id":'"$id"',"error":{"code":-32602,"message":"unknown uri"}}'
          ;;
      esac
      ;;
  esac
done
`

// flattenContent joins the text of every ContentText block — used by
// the tool tests below to assert on the rendered payload.
func flattenContent(p *engine.ToolResultPayload) string {
	var b strings.Builder
	for _, c := range p.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

func writeResourcefulServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-mcp-resources.sh")
	if err := os.WriteFile(path, []byte(resourcefulServer), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStdioListResources(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeResourcefulServer(t)
	c := NewStdio(StdioConfig{
		Name: "docs", Command: "/bin/sh", Args: []string{path},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := c.ListResources(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 resources, got %d (%+v)", len(got), got)
	}
	if got[0].URI != "docs://readme" || got[0].Name != "Readme" {
		t.Errorf("first resource shape wrong: %+v", got[0])
	}
}

func TestStdioReadResource(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeResourcefulServer(t)
	c := NewStdio(StdioConfig{
		Name: "docs", Command: "/bin/sh", Args: []string{path},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := c.ReadResource(ctx, "docs://readme")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(res.Contents) != 1 || res.Contents[0].MimeType != "text/markdown" {
		t.Errorf("unexpected payload: %+v", res)
	}
	if !strings.Contains(res.Contents[0].Text, "# Hello") {
		t.Errorf("body missing: %q", res.Contents[0].Text)
	}
}

func TestRegistryListResourcesAcrossServers(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "docs", Command: "/bin/sh", Args: []string{writeResourcefulServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Connect(ctx, StdioConfig{
		Name: "docs2", Command: "/bin/sh", Args: []string{writeResourcefulServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	got, errs := r.ListResources(ctx, "")
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	// 2 resources × 2 servers
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %d (%+v)", len(got), got)
	}
	// Server-filtered call returns only that server's catalog.
	got, errs = r.ListResources(ctx, "docs")
	if len(errs) > 0 || len(got) != 2 {
		t.Errorf("filtered list wrong: %v / %+v", errs, got)
	}
	// Unknown server short-circuits with a clear error.
	_, errs = r.ListResources(ctx, "ghost")
	if len(errs) == 0 {
		t.Errorf("filtering on unknown server should error")
	}
}

func TestRegistryReadResourceServerScopedAndScan(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "docs", Command: "/bin/sh", Args: []string{writeResourcefulServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	res, from, err := r.ReadResource(ctx, "docs", "docs://license")
	if err != nil {
		t.Fatalf("scoped read: %v", err)
	}
	if from != "docs" {
		t.Errorf("server label wrong: %q", from)
	}
	if res.Contents[0].Text != "MIT" {
		t.Errorf("payload: %+v", res)
	}
	// No-server scan finds it via the only server.
	res, from, err = r.ReadResource(ctx, "", "docs://license")
	if err != nil || from != "docs" {
		t.Errorf("scan read: from=%q err=%v", from, err)
	}
	// Scan + nonexistent URI surfaces the last error rather than
	// hanging or returning nil.
	if _, _, err = r.ReadResource(ctx, "", "docs://does-not-exist"); err == nil {
		t.Errorf("expected error for unknown uri")
	}
}

func TestListMcpResourcesToolRendersServerQualified(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "docs", Command: "/bin/sh", Args: []string{writeResourcefulServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	tool := listMcpResourcesTool{reg: r}
	out, _ := tool.Call(ctx, map[string]any{}, nil)
	if out.IsError {
		t.Fatalf("unexpected error result: %+v", out)
	}
	body := flattenContent(out)
	for _, want := range []string{"resources: 2", "[docs] docs://readme", "Readme", "text/markdown"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q in:\n%s", want, body)
		}
	}
}

func TestReadMcpResourceToolEmitsTextBody(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "docs", Command: "/bin/sh", Args: []string{writeResourcefulServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	tool := readMcpResourceTool{reg: r}
	out, _ := tool.Call(ctx, map[string]any{
		"server": "docs",
		"uri":    "docs://readme",
	}, nil)
	if out.IsError {
		t.Fatalf("unexpected error: %+v", out)
	}
	body := flattenContent(out)
	for _, want := range []string{"server: docs", "uri: docs://readme", "# Hello"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q in:\n%s", want, body)
		}
	}
}

func TestReadMcpResourceToolSoftErrorsOnMissingURI(t *testing.T) {
	tool := readMcpResourceTool{reg: NewRegistry()}
	out, _ := tool.Call(context.Background(), map[string]any{}, nil)
	if !out.IsError {
		t.Errorf("missing uri should soft-error; got %+v", out)
	}
}

func TestRegisterEngineToolsIncludesResourceTools(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "docs", Command: "/bin/sh", Args: []string{writeResourcefulServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	engReg := engine.NewRegistry()
	names := r.RegisterEngineTools(engReg)
	hasList := false
	hasRead := false
	for _, n := range names {
		if n == "ListMcpResources" {
			hasList = true
		}
		if n == "ReadMcpResource" {
			hasRead = true
		}
	}
	if !hasList || !hasRead {
		t.Errorf("expected resource tools in registration; got %v", names)
	}
}

// Empty registry does NOT install the resource tools — the model
// shouldn't see a tool that has nothing to call.
func TestRegisterEngineToolsSkipsResourceToolsWhenEmpty(t *testing.T) {
	r := NewRegistry()
	defer r.Close()
	engReg := engine.NewRegistry()
	names := r.RegisterEngineTools(engReg)
	for _, n := range names {
		if n == "ListMcpResources" || n == "ReadMcpResource" {
			t.Errorf("empty registry should not install %s", n)
		}
	}
}

// Sanity check that the protocol types serialize the way the tests
// above implicitly assume — guards against silent JSON tag drift.
func TestResourceJSONRoundTrip(t *testing.T) {
	r := Resource{URI: "x://y", Name: "n", MimeType: "text/plain", Description: "d"}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"uri":"x://y"`) ||
		!strings.Contains(string(b), `"mimeType":"text/plain"`) {
		t.Errorf("Resource encoded oddly: %s", b)
	}
	rc := ResourceContent{URI: "x://y", Text: "hello"}
	b, _ = json.Marshal(rc)
	if !strings.Contains(string(b), `"text":"hello"`) {
		t.Errorf("ResourceContent encoded oddly: %s", b)
	}
}
