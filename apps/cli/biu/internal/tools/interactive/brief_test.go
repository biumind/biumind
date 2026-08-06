package interactive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

type mockNotifier struct {
	calls []string
	err   error
}

func (m *mockNotifier) Notify(_ context.Context, msg string) error {
	m.calls = append(m.calls, msg)
	return m.err
}

// TestBrief_Normal_NoNotifierFire — status=normal must NOT fire the
// notifier even when one is wired.
func TestBrief_Normal_NoNotifierFire(t *testing.T) {
	n := &mockNotifier{}
	tool := BriefTool{Notifier: n}
	res, _ := tool.Call(context.Background(), map[string]any{
		"message": "all green",
		"status":  "normal",
	}, &engine.ToolEnv{})
	if res.IsError {
		t.Fatalf("brief failed: %s", flatten(res))
	}
	if len(n.calls) != 0 {
		t.Errorf("normal should not fire notifier; got %v", n.calls)
	}
}

// TestBrief_Proactive_FiresNotifier — status=proactive fires the
// notifier when one is wired; in result body we get a confirmation
// note.
func TestBrief_Proactive_FiresNotifier(t *testing.T) {
	n := &mockNotifier{}
	tool := BriefTool{Notifier: n}
	res, _ := tool.Call(context.Background(), map[string]any{
		"message": "build broken",
		"status":  "proactive",
	}, &engine.ToolEnv{})
	if len(n.calls) != 1 || n.calls[0] != "build broken" {
		t.Errorf("proactive should fire notifier; got %v", n.calls)
	}
	if !strings.Contains(flatten(res), "notification sent") {
		t.Errorf("expected confirmation in result: %s", flatten(res))
	}
}

// TestBrief_Proactive_NoNotifier_StillWorks — proactive with nil
// notifier still completes (no desktop ping is fine; the message
// still surfaces in conversation).
func TestBrief_Proactive_NoNotifier_StillWorks(t *testing.T) {
	tool := BriefTool{Notifier: nil}
	res, _ := tool.Call(context.Background(), map[string]any{
		"message": "x",
		"status":  "proactive",
	}, &engine.ToolEnv{})
	if res.IsError {
		t.Errorf("proactive without notifier should still succeed")
	}
}

// TestBrief_NotifierError_Surfaces — notifier failure is recorded in
// the result body but the tool itself doesn't error.
func TestBrief_NotifierError_Surfaces(t *testing.T) {
	n := &mockNotifier{err: errors.New("dbus down")}
	tool := BriefTool{Notifier: n}
	res, _ := tool.Call(context.Background(), map[string]any{
		"message": "x",
		"status":  "proactive",
	}, &engine.ToolEnv{})
	if res.IsError {
		t.Errorf("notifier failure should not fail the tool")
	}
	if !strings.Contains(flatten(res), "notification failed") ||
		!strings.Contains(flatten(res), "dbus down") {
		t.Errorf("notifier error should surface in body: %s", flatten(res))
	}
}

// TestBrief_MissingFields — message and status are both required.
func TestBrief_MissingFields(t *testing.T) {
	tool := BriefTool{}
	r1, _ := tool.Call(context.Background(), map[string]any{
		"status": "normal",
	}, &engine.ToolEnv{})
	if !r1.IsError || !strings.Contains(flatten(r1), "message is required") {
		t.Errorf("missing message should soft-error")
	}
	r2, _ := tool.Call(context.Background(), map[string]any{
		"message": "x",
	}, &engine.ToolEnv{})
	if !r2.IsError || !strings.Contains(flatten(r2), "status is required") {
		t.Errorf("missing status should soft-error")
	}
	r3, _ := tool.Call(context.Background(), map[string]any{
		"message": "x", "status": "panic",
	}, &engine.ToolEnv{})
	if !r3.IsError || !strings.Contains(flatten(r3), "unknown status") {
		t.Errorf("unknown status should soft-error")
	}
}

// TestBrief_AttachmentsResolved — relative paths resolve against
// env.Cwd; missing files are tagged "missing"; existing files get a
// byte count.
func TestBrief_AttachmentsResolved(t *testing.T) {
	tmp := t.TempDir()
	good := filepath.Join(tmp, "log.txt")
	if err := os.WriteFile(good, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := BriefTool{}
	res, _ := tool.Call(context.Background(), map[string]any{
		"message": "see log",
		"status":  "normal",
		"attachments": []any{
			"log.txt",
			"missing.txt",
		},
	}, &engine.ToolEnv{Cwd: tmp})
	body := flatten(res)
	if !strings.Contains(body, good+" (ok, 5 bytes)") {
		t.Errorf("good attachment missing: %s", body)
	}
	if !strings.Contains(body, "missing.txt") || !strings.Contains(body, "missing") {
		t.Errorf("missing attachment should be flagged: %s", body)
	}
}
