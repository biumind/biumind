// End-to-end tests for prompts/list + prompts/get. Drives a fake
// MCP server through StdioClient and Registry surfaces; mirrors the
// resources_test.go harness but exercises the prompts methods.

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// promptyServer is a fake MCP server that exposes two prompt
// templates. `summarise` ignores arguments; `greet` echoes the
// `who` argument back into the rendered text. Differs from the
// other fake servers by handling prompts/list + prompts/get.
const promptyServer = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"prompty","version":"0.1"},"capabilities":{"tools":{},"prompts":{}}}}'
      ;;
    *'"method":"notifications/initialized"'*)
      ;;
    *'"method":"tools/list"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"tools":[]}}'
      ;;
    *'"method":"prompts/list"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"prompts":[{"name":"summarise","description":"Summarise the project"},{"name":"greet","description":"Greet someone","arguments":[{"name":"who","required":true}]}]}}'
      ;;
    *'"method":"prompts/get"'*)
      id=$(echo "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      pname=$(echo "$line" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
      case "$pname" in
        summarise)
          echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"description":"Summary prompt","messages":[{"role":"user","content":{"type":"text","text":"Please summarise"}}]}}'
          ;;
        greet)
          who=$(echo "$line" | sed -n 's/.*"who":"\([^"]*\)".*/\1/p')
          echo '{"jsonrpc":"2.0","id":'"$id"',"result":{"description":"Greeting","messages":[{"role":"user","content":{"type":"text","text":"Hello '"$who"'"}}]}}'
          ;;
        *)
          echo '{"jsonrpc":"2.0","id":'"$id"',"error":{"code":-32602,"message":"unknown prompt"}}'
          ;;
      esac
      ;;
  esac
done
`

func writePromptyServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-mcp-prompts.sh")
	if err := os.WriteFile(path, []byte(promptyServer), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStdioListPrompts(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	c := NewStdio(StdioConfig{
		Name: "prompty", Command: "/bin/sh", Args: []string{writePromptyServer(t)},
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
	got, err := c.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 prompts, got %d (%+v)", len(got), got)
	}
	// `greet` carries one argument; verify the schema flowed through.
	var greet *Prompt
	for i := range got {
		if got[i].Name == "greet" {
			greet = &got[i]
			break
		}
	}
	if greet == nil {
		t.Fatalf("missing greet prompt: %+v", got)
	}
	if len(greet.Arguments) != 1 || greet.Arguments[0].Name != "who" || !greet.Arguments[0].Required {
		t.Errorf("greet arguments wrong: %+v", greet.Arguments)
	}
}

func TestStdioGetPromptWithArguments(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	c := NewStdio(StdioConfig{
		Name: "prompty", Command: "/bin/sh", Args: []string{writePromptyServer(t)},
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
	res, err := c.GetPrompt(ctx, "greet", map[string]string{"who": "world"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(res.Messages))
	}
	if res.Messages[0].Role != "user" {
		t.Errorf("role wrong: %q", res.Messages[0].Role)
	}
	if !strings.Contains(res.Messages[0].Content.Text, "Hello world") {
		t.Errorf("argument substitution missing: %q", res.Messages[0].Content.Text)
	}
}

func TestRegistryListPromptsScoped(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "prompty", Command: "/bin/sh", Args: []string{writePromptyServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	got, errs := r.ListPrompts(ctx, "prompty")
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 prompts, got %d (%+v)", len(got), got)
	}
	if got[0].Server != "prompty" {
		t.Errorf("server label missing: %+v", got[0])
	}
	// Unknown server short-circuits.
	if _, errs = r.ListPrompts(ctx, "ghost"); len(errs) == 0 {
		t.Errorf("filtering on unknown server should error")
	}
}

func TestRegistryGetPromptScanFallsThrough(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "prompty", Command: "/bin/sh", Args: []string{writePromptyServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	// server="" forces the scan path; only one server has the prompt
	// so the lookup picks it.
	res, from, err := r.GetPrompt(ctx, "", "summarise", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if from != "prompty" {
		t.Errorf("server label wrong: %q", from)
	}
	if len(res.Messages) != 1 || !strings.Contains(res.Messages[0].Content.Text, "summarise") {
		t.Errorf("body wrong: %+v", res)
	}
	// Unknown name in scan mode reports the server's last error.
	if _, _, err = r.GetPrompt(ctx, "", "nope", nil); err == nil {
		t.Errorf("unknown prompt should error")
	}
}

func TestListMcpPromptsToolRendersArgs(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "prompty", Command: "/bin/sh", Args: []string{writePromptyServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	tool := listMcpPromptsTool{reg: r}
	out, _ := tool.Call(ctx, map[string]any{}, nil)
	if out.IsError {
		t.Fatalf("unexpected error: %+v", out)
	}
	body := flattenContent(out)
	for _, want := range []string{
		"prompts: 2",
		"[prompty] summarise",
		"[prompty] greet",
		"arg who (required)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q in:\n%s", want, body)
		}
	}
}

func TestGetMcpPromptToolRendersMessages(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "prompty", Command: "/bin/sh", Args: []string{writePromptyServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	tool := getMcpPromptTool{reg: r}
	out, _ := tool.Call(ctx, map[string]any{
		"server":    "prompty",
		"name":      "greet",
		"arguments": map[string]any{"who": "biu"},
	}, nil)
	if out.IsError {
		t.Fatalf("unexpected error: %+v", out)
	}
	body := flattenContent(out)
	for _, want := range []string{
		"server: prompty",
		"prompt: greet",
		"messages: 1",
		"--- message 1 (user)",
		"Hello biu",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q in:\n%s", want, body)
		}
	}
}

func TestGetMcpPromptToolMissingNameSoftErrors(t *testing.T) {
	tool := getMcpPromptTool{reg: NewRegistry()}
	out, _ := tool.Call(context.Background(), map[string]any{}, nil)
	if !out.IsError {
		t.Errorf("missing name should soft-error; got %+v", out)
	}
}

// RegisterEngineTools must include the prompt tools when at least
// one server is connected (mirrors the resource-tools gating logic).
func TestRegisterEngineToolsIncludesPromptTools(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Connect(ctx, StdioConfig{
		Name: "prompty", Command: "/bin/sh", Args: []string{writePromptyServer(t)},
	}); err != nil {
		t.Fatal(err)
	}
	engReg := engine.NewRegistry()
	names := r.RegisterEngineTools(engReg)
	hasList := false
	hasGet := false
	for _, n := range names {
		if n == "ListMcpPrompts" {
			hasList = true
		}
		if n == "GetMcpPrompt" {
			hasGet = true
		}
	}
	if !hasList || !hasGet {
		t.Errorf("expected prompt tools in registration; got %v", names)
	}
}
