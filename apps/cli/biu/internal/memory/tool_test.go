package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// stagedTool returns a MemoryTool pointing at a t.TempDir-backed
// AutoMemory. Lets each test get its own isolated directory.
func stagedTool(t *testing.T) (MemoryTool, string, string) {
	t.Helper()
	home := t.TempDir()
	auto := LoadAuto(home)
	return MemoryTool{Auto: auto}, auto.Dir, home
}

func flatten(p *engine.ToolResultPayload) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range p.Content {
		b.WriteString(blk.Text)
	}
	return b.String()
}

// TestMemoryTool_Save_RoundTrip — happy path: save creates the file
// + updates MEMORY.md; the resulting state is loadable.
func TestMemoryTool_Save_RoundTrip(t *testing.T) {
	tool, dir, _ := stagedTool(t)
	res, err := tool.Call(context.Background(), map[string]any{
		"action":      "save",
		"memory_type": "user",
		"name":        "Go expert new to React",
		"description": "deep Go expertise; first time on this repo's frontend",
		"body":        "User has 10 years of Go but is new to React. Frame frontend explanations in terms of backend analogues.",
	}, nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("save should succeed: %s", flatten(res))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) < 2 {
		t.Errorf("expected MEMORY.md + at least one memory file; got %v", entries)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if !strings.Contains(string(idx), "Go expert") {
		t.Errorf("index should contain memory name; got:\n%s", idx)
	}
}

// TestMemoryTool_Save_MissingType — soft error when memory_type is
// missing or invalid.
func TestMemoryTool_Save_MissingType(t *testing.T) {
	tool, _, _ := stagedTool(t)
	res, _ := tool.Call(context.Background(), map[string]any{
		"action": "save",
		"body":   "something",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "memory_type required") {
		t.Errorf("missing type should soft-error: %s", flatten(res))
	}

	res2, _ := tool.Call(context.Background(), map[string]any{
		"action":      "save",
		"memory_type": "bogus",
		"body":        "x",
	}, nil)
	if !res2.IsError {
		t.Errorf("invalid type should soft-error")
	}
}

// TestMemoryTool_Save_MissingBody — body is required.
func TestMemoryTool_Save_MissingBody(t *testing.T) {
	tool, _, _ := stagedTool(t)
	res, _ := tool.Call(context.Background(), map[string]any{
		"action":      "save",
		"memory_type": "user",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "body is required") {
		t.Errorf("missing body should soft-error: %s", flatten(res))
	}
}

// TestMemoryTool_DefaultActionIsSave — input without action defaults
// to save (matches the system-prompt contract: "save a memory" is
// the typical request).
func TestMemoryTool_DefaultActionIsSave(t *testing.T) {
	tool, _, _ := stagedTool(t)
	res, _ := tool.Call(context.Background(), map[string]any{
		"memory_type": "user",
		"body":        "default-action save",
	}, nil)
	if res.IsError {
		t.Errorf("default action should be save; got error: %s", flatten(res))
	}
}

// TestMemoryTool_List_EmptyDir — list against an empty dir returns
// the helpful "no memories yet" notice rather than an error.
func TestMemoryTool_List_EmptyDir(t *testing.T) {
	tool, _, _ := stagedTool(t)
	res, _ := tool.Call(context.Background(), map[string]any{
		"action": "list",
	}, nil)
	if res.IsError {
		t.Errorf("list on empty dir should not error")
	}
	if !strings.Contains(flatten(res), "No memories yet") {
		t.Errorf("expected helpful empty notice: %s", flatten(res))
	}
}

// TestMemoryTool_List_AfterSave — list after save returns the index
// plus the basenames of the memory files.
func TestMemoryTool_List_AfterSave(t *testing.T) {
	tool, _, home := stagedTool(t)
	_, _ = tool.Call(context.Background(), map[string]any{
		"action":      "save",
		"memory_type": "feedback",
		"body":        "User dislikes trailing summaries.",
	}, nil)
	tool.Auto = LoadAuto(home)

	res, _ := tool.Call(context.Background(), map[string]any{
		"action": "list",
	}, nil)
	body := flatten(res)
	if !strings.Contains(body, "MEMORY.md") {
		t.Errorf("list output should reference MEMORY.md: %s", body)
	}
	if !strings.Contains(body, "Memory files on disk:") {
		t.Errorf("list should enumerate files: %s", body)
	}
	if !strings.Contains(body, "feedback-") {
		t.Errorf("list should mention the feedback file slug: %s", body)
	}
}

// TestMemoryTool_Remove_MissingFile — remove with no `file` is a
// soft error directing the model to use list first.
func TestMemoryTool_Remove_MissingFile(t *testing.T) {
	tool, _, _ := stagedTool(t)
	res, _ := tool.Call(context.Background(), map[string]any{
		"action": "remove",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "file is required") {
		t.Errorf("missing file should soft-error with hint: %s", flatten(res))
	}
}

// TestMemoryTool_Remove_PathTraversalRejected — basename hygiene
// (paranoid: the model could pass "../../etc/passwd").
func TestMemoryTool_Remove_PathTraversalRejected(t *testing.T) {
	tool, _, _ := stagedTool(t)
	for _, bad := range []string{
		"../escape.md",
		"sub/dir.md",
		"MEMORY.md",
	} {
		res, _ := tool.Call(context.Background(), map[string]any{
			"action": "remove", "file": bad,
		}, nil)
		if !res.IsError {
			t.Errorf("unsafe basename %q should soft-error", bad)
		}
	}
}

// TestMemoryTool_Remove_NotFound — non-existent file is a soft error.
func TestMemoryTool_Remove_NotFound(t *testing.T) {
	tool, _, _ := stagedTool(t)
	_, _ = tool.Auto.EnsureDir()
	res, _ := tool.Call(context.Background(), map[string]any{
		"action": "remove", "file": "user-not-real-20260101-000000.md",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "not found") {
		t.Errorf("missing file should soft-error: %s", flatten(res))
	}
}

// TestMemoryTool_Remove_HappyPath — save then remove, verify the
// file is gone AND MEMORY.md no longer references it.
func TestMemoryTool_Remove_HappyPath(t *testing.T) {
	tool, dir, home := stagedTool(t)
	saveRes, _ := tool.Call(context.Background(), map[string]any{
		"action":      "save",
		"memory_type": "user",
		"body":        "memory-to-delete",
	}, nil)
	if saveRes.IsError {
		t.Fatalf("setup save failed: %s", flatten(saveRes))
	}

	entries, _ := os.ReadDir(dir)
	var basename string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") &&
			e.Name() != "MEMORY.md" {
			basename = e.Name()
			break
		}
	}
	if basename == "" {
		t.Fatalf("could not find saved memory file in %s", dir)
	}

	tool.Auto = LoadAuto(home)

	res, _ := tool.Call(context.Background(), map[string]any{
		"action": "remove", "file": basename,
	}, nil)
	if res.IsError {
		t.Fatalf("remove failed: %s", flatten(res))
	}
	if !strings.Contains(flatten(res), "Pruned") {
		t.Errorf("remove should report index prune: %s", flatten(res))
	}
	if _, err := os.Stat(filepath.Join(dir, basename)); !os.IsNotExist(err) {
		t.Errorf("file should be gone; stat err = %v", err)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if strings.Contains(string(idx), basename) {
		t.Errorf("index still references deleted basename:\n%s", idx)
	}
}

// TestMemoryTool_UnknownAction — the model could pass typo'd actions.
func TestMemoryTool_UnknownAction(t *testing.T) {
	tool, _, _ := stagedTool(t)
	res, _ := tool.Call(context.Background(), map[string]any{
		"action": "delete",
	}, nil)
	if !res.IsError {
		t.Errorf("unknown action should soft-error")
	}
}

// TestMemoryTool_NoHomeDir — when Auto.Dir is empty (HOME unresolved
// in some CI containers), the tool soft-errors instead of panicking.
func TestMemoryTool_NoHomeDir(t *testing.T) {
	tool := MemoryTool{Auto: AutoMemory{}}
	res, _ := tool.Call(context.Background(), map[string]any{
		"action":      "save",
		"memory_type": "user",
		"body":        "x",
	}, nil)
	if !res.IsError || !strings.Contains(flatten(res), "directory unresolved") {
		t.Errorf("empty Auto.Dir should soft-error with helpful text: %s", flatten(res))
	}
}

// TestMemoryTool_DeclarativeFlags — the read-only / destructive
// attributes flip based on action so the engine's batch scheduler
// can group reads together.
func TestMemoryTool_DeclarativeFlags(t *testing.T) {
	tool := MemoryTool{}
	if !tool.IsReadOnly(map[string]any{"action": "list"}) {
		t.Errorf("list should be read-only")
	}
	if tool.IsReadOnly(map[string]any{"action": "save"}) {
		t.Errorf("save should NOT be read-only")
	}
	if !tool.IsDestructive(map[string]any{"action": "remove"}) {
		t.Errorf("remove should be destructive")
	}
	if tool.IsDestructive(map[string]any{"action": "save"}) {
		t.Errorf("save should NOT be destructive")
	}
}
