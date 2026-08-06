package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestCreateAndGet(t *testing.T) {
	a := New()
	ctx := context.Background()

	out, err := a.Invoke(ctx, "create", mustJSON(t, map[string]any{
		"owner_id": "u1",
		"title":    "Buy milk",
		"tags":     []string{"home", "errand"},
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tk, ok := out.(*Task)
	if !ok || !strings.HasPrefix(tk.ID, "tsk-") {
		t.Fatalf("bad output: %+v", out)
	}
	if tk.Title != "Buy milk" || tk.OwnerID != "u1" {
		t.Errorf("fields: %+v", tk)
	}

	got, err := a.Invoke(ctx, "get", mustJSON(t, map[string]any{"id": tk.ID}))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if g := got.(*Task); g.ID != tk.ID || len(g.Tags) != 2 {
		t.Errorf("get mismatch: %+v", g)
	}
}

func TestCreateRejectsEmptyTitle(t *testing.T) {
	a := New()
	if _, err := a.Invoke(context.Background(), "create",
		json.RawMessage(`{"title":""}`)); err == nil ||
		!strings.Contains(err.Error(), "missing title") {
		t.Errorf("want missing-title err, got %v", err)
	}
}

func TestCompleteAndDelete(t *testing.T) {
	a := New()
	ctx := context.Background()
	out, _ := a.Invoke(ctx, "create", json.RawMessage(`{"title":"x"}`))
	id := out.(*Task).ID

	if _, err := a.Invoke(ctx, "complete", mustJSON(t, map[string]any{"id": id})); err != nil {
		t.Fatalf("complete: %v", err)
	}
	got, _ := a.Invoke(ctx, "get", mustJSON(t, map[string]any{"id": id}))
	if !got.(*Task).Done {
		t.Errorf("complete did not flip Done")
	}

	if _, err := a.Invoke(ctx, "delete", mustJSON(t, map[string]any{"id": id})); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := a.Invoke(ctx, "get", mustJSON(t, map[string]any{"id": id})); err == nil {
		t.Errorf("get after delete should fail")
	}
}

func TestListFiltersByTagAndDone(t *testing.T) {
	a := New()
	ctx := context.Background()
	mk := func(title string, tags []string, done bool) {
		out, _ := a.Invoke(ctx, "create", mustJSON(t, map[string]any{"title": title, "tags": tags}))
		if done {
			_, _ = a.Invoke(ctx, "complete", mustJSON(t, map[string]any{"id": out.(*Task).ID}))
		}
	}
	mk("a", []string{"x"}, false)
	mk("b", []string{"x", "y"}, true)
	mk("c", []string{"y"}, false)

	out, _ := a.Invoke(ctx, "list", json.RawMessage(`{"tag":"x"}`))
	if len(out.(*listOut).Tasks) != 2 {
		t.Errorf("tag=x should match 2: %+v", out)
	}

	doneFalse := false
	out, _ = a.Invoke(ctx, "list", mustJSON(t, map[string]any{"done": doneFalse}))
	for _, tk := range out.(*listOut).Tasks {
		if tk.Done {
			t.Errorf("done=false filter leaked: %+v", tk)
		}
	}
}

func TestNewWithFilePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tasks.json"
	ctx := context.Background()

	a, err := NewWithFile(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	out, err := a.Invoke(ctx, "create", mustJSON(t, map[string]any{
		"title": "persist me", "tags": []string{"x"},
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := out.(*Task).ID

	// New instance against the same path should see the row.
	b, err := NewWithFile(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err := b.Invoke(ctx, "get", mustJSON(t, map[string]any{"id": id}))
	if err != nil {
		t.Fatalf("get after reload: %v", err)
	}
	if got.(*Task).Title != "persist me" {
		t.Errorf("reload mismatch: %+v", got)
	}

	// Delete in the new instance, reload again — should be gone.
	if _, err := b.Invoke(ctx, "delete", mustJSON(t, map[string]any{"id": id})); err != nil {
		t.Fatalf("delete: %v", err)
	}
	c, _ := NewWithFile(path)
	if _, err := c.Invoke(ctx, "get", mustJSON(t, map[string]any{"id": id})); err == nil {
		t.Errorf("delete didn't persist")
	}
}

func TestNewWithFileMissingFileIsOK(t *testing.T) {
	dir := t.TempDir()
	a, err := NewWithFile(dir + "/never-existed.json")
	if err != nil {
		t.Fatalf("missing file should be ok, got %v", err)
	}
	out, _ := a.Invoke(context.Background(), "list", json.RawMessage(`{}`))
	if len(out.(*listOut).Tasks) != 0 {
		t.Errorf("empty store expected: %+v", out)
	}
}

func TestUnknownActionFails(t *testing.T) {
	a := New()
	if _, err := a.Invoke(context.Background(), "ohno", json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "unknown action") {
		t.Errorf("want unknown-action err, got %v", err)
	}
}

func TestPendingCountBadge(t *testing.T) {
	a := New()
	ctx := context.Background()

	// 空 store → count=0 / info。
	out0, err := a.Invoke(ctx, "pending_count", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("pending_count: %v", err)
	}
	m0 := out0.(map[string]any)
	if m0["count"].(int) != 0 || m0["severity"] != "info" {
		t.Errorf("empty: %+v", m0)
	}

	// 加 3 个未完成 + 1 个完成。
	for i := 0; i < 3; i++ {
		_, _ = a.Invoke(ctx, "create", mustJSON(t, map[string]any{
			"title": "todo",
		}))
	}
	doneOut, _ := a.Invoke(ctx, "create", mustJSON(t, map[string]any{"title": "done"}))
	donedID := doneOut.(*Task).ID
	_, _ = a.Invoke(ctx, "complete", mustJSON(t, map[string]any{"id": donedID}))

	out1, _ := a.Invoke(ctx, "pending_count", json.RawMessage(`{}`))
	m1 := out1.(map[string]any)
	if m1["count"].(int) != 3 {
		t.Errorf("count = %v, want 3", m1["count"])
	}
	if m1["severity"] != "info" {
		t.Errorf("severity at 3 = %v, want info", m1["severity"])
	}

	// ≥10 → warn。
	for i := 0; i < 8; i++ {
		_, _ = a.Invoke(ctx, "create", mustJSON(t, map[string]any{"title": "todo"}))
	}
	out2, _ := a.Invoke(ctx, "pending_count", json.RawMessage(`{}`))
	m2 := out2.(map[string]any)
	if m2["count"].(int) < 10 {
		t.Fatalf("setup error: %+v", m2)
	}
	if m2["severity"] != "warn" {
		t.Errorf("severity at ≥10 = %v, want warn", m2["severity"])
	}

	// 任一逾期 → error。
	_, _ = a.Invoke(ctx, "create", mustJSON(t, map[string]any{
		"title": "overdue",
		"due":   time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
	}))
	out3, _ := a.Invoke(ctx, "pending_count", json.RawMessage(`{}`))
	if out3.(map[string]any)["severity"] != "error" {
		t.Errorf("with overdue, severity = %v, want error", out3.(map[string]any)["severity"])
	}
}

func TestManifestSidebarBadge(t *testing.T) {
	m := New().Manifest()
	if m.Sidebar == nil {
		t.Fatal("sidebar missing")
	}
	if m.Sidebar.BadgeAction != "pending_count" {
		t.Errorf("BadgeAction = %q", m.Sidebar.BadgeAction)
	}
	if m.Sidebar.BadgeRefreshSec < 60 {
		t.Errorf("BadgeRefreshSec = %d, want ≥ 60", m.Sidebar.BadgeRefreshSec)
	}
}
