// Tests for Registry.Servers() — the introspection surface the
// REPL's /mcp slash relies on. We seed the registry's private
// state directly because launching real stdio MCP servers from
// every test would be slow and OS-dependent. The Registry public
// API stays the contract; this test lives in the same package so
// it can manipulate the internal maps for fixture purposes.

package mcp

import (
	"testing"
)

// seedServer wires a fake StdioClient + tool entries into the
// registry without going through Connect (which would actually
// launch a process). The fake client only needs a non-nil cfg so
// Servers() can read its Command + Args.
func seedServer(r *Registry, name, command string, args []string, tools []ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[name] = &StdioClient{cfg: StdioConfig{
		Name: name, Command: command, Args: append([]string(nil), args...),
	}}
	for _, t := range tools {
		safe := NormalizeToolName(t.Name)
		q := QualifyName(name, safe)
		r.tools[q] = &RegisteredTool{
			QualifiedName: q,
			Server:        name,
			OriginalName:  t.Name,
			Def:           t,
		}
	}
}

func TestServersEmpty(t *testing.T) {
	r := NewRegistry()
	if got := r.Servers(); len(got) != 0 {
		t.Errorf("empty registry should yield no servers; got %v", got)
	}
}

func TestServersSnapshotShape(t *testing.T) {
	r := NewRegistry()
	seedServer(r, "github", "npx", []string{"-y", "github-mcp"},
		[]ToolDef{
			{Name: "create_pr", Description: "Open a PR"},
			{Name: "list_issues", Description: "List repo issues"},
		})

	got := r.Servers()
	if len(got) != 1 {
		t.Fatalf("expected 1 server; got %d", len(got))
	}
	s := got[0]
	if s.Name != "github" {
		t.Errorf("Name: got %q", s.Name)
	}
	if s.Command != "npx" {
		t.Errorf("Command: got %q", s.Command)
	}
	if len(s.Args) != 2 || s.Args[0] != "-y" {
		t.Errorf("Args: got %v", s.Args)
	}
	if s.ToolCount != 2 {
		t.Errorf("ToolCount: got %d", s.ToolCount)
	}
	// Per-server tools sorted by qualified name.
	if len(s.Tools) != 2 {
		t.Fatalf("Tools length: %d", len(s.Tools))
	}
	if s.Tools[0].QualifiedName > s.Tools[1].QualifiedName {
		t.Errorf("Tools not sorted: %v", s.Tools)
	}
	// Each tool must include the description verbatim.
	descs := map[string]string{}
	for _, tool := range s.Tools {
		descs[tool.QualifiedName] = tool.Description
	}
	if descs["mcp__github__create_pr"] != "Open a PR" {
		t.Errorf("descriptions wrong: %v", descs)
	}
}

// Multiple servers must appear sorted by name — keeps the /mcp
// list rendering stable across runs / map ordering.
func TestServersSortedByName(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"zebra", "alpha", "mango"} {
		seedServer(r, n, "true", nil, []ToolDef{{Name: "noop"}})
	}
	got := r.Servers()
	want := []string{"alpha", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("server %d: got %q want %q", i, got[i].Name, want[i])
		}
	}
}

// Tools that hit upstream names with non-safe chars (`.`, `/`)
// should expose the original under OriginalName so the user can
// trace `mcp__server__file_read` back to whatever the upstream
// called the tool.
func TestServersExposeOriginalName(t *testing.T) {
	r := NewRegistry()
	seedServer(r, "fs", "fs-mcp", nil, []ToolDef{
		{Name: "file.read"},
	})
	got := r.Servers()
	if len(got) != 1 {
		t.Fatalf("setup: %d servers", len(got))
	}
	tool := got[0].Tools[0]
	if tool.QualifiedName != "mcp__fs__file_read" {
		t.Errorf("qualified name not normalised: %q", tool.QualifiedName)
	}
	if tool.OriginalName != "file.read" {
		t.Errorf("original name lost: %q", tool.OriginalName)
	}
}
