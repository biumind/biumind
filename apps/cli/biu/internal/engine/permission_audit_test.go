// Tests for the auto-mode permission audit trail
// (PermissionAuditEvent, runner.go askPermission).
//
// Contract:
//   - auto-mode-approve firing           → exactly one "auto-approved"
//   - dangerous-command guard escalation → exactly one "guard-ask"
//   - sensitive-path guard escalation    → exactly one "guard-ask"
//   - manual mode (allow OR ask)         → no audit at all
//   - deny (rule veto inside auto mode)  → no audit; the denial
//     already surfaces as an error tool_result

package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

// drainAnswering collects every event, auto-answering permission asks
// with the given decision so the turn can complete.
func drainAnswering(ch <-chan Event, d PermissionDecision) []Event {
	var out []Event
	for ev := range ch {
		out = append(out, ev)
		if a, ok := ev.(*PermissionAskEvent); ok {
			a.Decision <- PermissionAnswer{Decision: d}
		}
	}
	return out
}

func auditEvents(events []Event) []*PermissionAuditEvent {
	var out []*PermissionAuditEvent
	for _, ev := range events {
		if a, ok := ev.(*PermissionAuditEvent); ok {
			out = append(out, a)
		}
	}
	return out
}

func newAuditEngine(t *testing.T, perms *permissions.Context, tool Tool, inputJSON string) *QueryEngine {
	t.Helper()
	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("working", "tu_1", tool.Name(), inputJSON),
		textTurn("done"),
	}}
	reg := NewRegistry()
	reg.Register(tool)
	eng, err := New(Options{
		State: state.New(), Tools: reg, Provider: prov, Model: "test",
		Permissions: perms,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestPermissionAudit_AutoApprove(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	bash := &fakeTool{name: "Bash", destructive: true}
	eng := newAuditEngine(t, perms, bash, `{"command":"rm -rf /tmp/x"}`)

	events := drainAnswering(eng.Submit(context.Background(), "clean up"), PermAllow)

	audits := auditEvents(events)
	if len(audits) != 1 {
		t.Fatalf("expected exactly 1 audit event; got %d (%v)", len(audits), audits)
	}
	a := audits[0]
	if a.Outcome != "auto-approved" {
		t.Errorf("Outcome = %q, want auto-approved", a.Outcome)
	}
	if a.Policy != "auto-mode-approve" {
		t.Errorf("Policy = %q, want auto-mode-approve", a.Policy)
	}
	if a.ToolName != "Bash" || a.ToolUseID != "tu_1" {
		t.Errorf("tool identity lost: %+v", a)
	}
	if cmd, _ := a.Input["command"].(string); cmd != "rm -rf /tmp/x" {
		t.Errorf("Input should carry the original args; got %+v", a.Input)
	}
	if hasEvent(events, func(*PermissionAskEvent) bool { return true }) {
		t.Error("auto-approved call must not produce a permission ask")
	}
	if bash.calls != 1 {
		t.Errorf("tool should have run; calls=%d", bash.calls)
	}
}

func TestPermissionAudit_DangerousCommandGuardAsk(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	bash := &warningTool{
		fakeTool: &fakeTool{name: "Bash", destructive: true},
		warnings: []string{"runs as root (sudo)"},
	}
	eng := newAuditEngine(t, perms, bash, `{"command":"sudo apt update"}`)

	events := drainAnswering(eng.Submit(context.Background(), "sudo thing"), PermAllow)

	audits := auditEvents(events)
	if len(audits) != 1 {
		t.Fatalf("expected exactly 1 audit event; got %d", len(audits))
	}
	a := audits[0]
	if a.Outcome != "guard-ask" {
		t.Errorf("Outcome = %q, want guard-ask", a.Outcome)
	}
	if a.Policy != "dangerous-command-ask" {
		t.Errorf("Policy = %q, want dangerous-command-ask", a.Policy)
	}
	if !strings.Contains(a.Reason, "dangerous command guard") ||
		!strings.Contains(a.Reason, "runs as root") {
		t.Errorf("Reason should carry the guard text; got %q", a.Reason)
	}
	// The guard still escalates to a real ask — audit is additive,
	// not a replacement for the dialog.
	if !hasEvent(events, func(*PermissionAskEvent) bool { return true }) {
		t.Error("guard escalation must still emit PermissionAskEvent")
	}
}

func TestPermissionAudit_SensitivePathGuardAsk(t *testing.T) {
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	// No Warner warnings — the sensitive-path guard fires on the
	// file_path arg alone.
	write := &fakeTool{name: "Write", destructive: true}
	eng := newAuditEngine(t, perms, write, `{"file_path":".env","content":"X=1"}`)

	events := drainAnswering(eng.Submit(context.Background(), "write env"), PermAllow)

	audits := auditEvents(events)
	if len(audits) != 1 {
		t.Fatalf("expected exactly 1 audit event; got %d", len(audits))
	}
	if audits[0].Outcome != "guard-ask" || audits[0].Policy != "sensitive-path-ask" {
		t.Errorf("got %+v, want guard-ask via sensitive-path-ask", audits[0])
	}
	if !strings.Contains(audits[0].Reason, ".env") {
		t.Errorf("Reason should name the sensitive path; got %q", audits[0].Reason)
	}
}

func TestPermissionAudit_ManualModeSilent(t *testing.T) {
	// Default (manual) mode: a read-only call auto-allows via the
	// readOnly policy — no audit; a destructive call lands on the
	// fallback ask — the dialog is the record, still no audit.
	read := &fakeTool{name: "Read", readOnly: true, concurrencySafe: true}
	eng := newAuditEngine(t, permissions.NewContext(), read, `{"path":"/a"}`)
	events := drainAnswering(eng.Submit(context.Background(), "read x"), PermAllow)
	if n := len(auditEvents(events)); n != 0 {
		t.Errorf("manual mode read-only allow must not audit; got %d", n)
	}

	bash := &fakeTool{name: "Bash", destructive: true}
	eng = newAuditEngine(t, permissions.NewContext(), bash, `{"command":"rm -rf /tmp/x"}`)
	events = drainAnswering(eng.Submit(context.Background(), "rm x"), PermAllow)
	if n := len(auditEvents(events)); n != 0 {
		t.Errorf("manual mode ask must not audit; got %d", n)
	}
	if !hasEvent(events, func(*PermissionAskEvent) bool { return true }) {
		t.Error("manual mode destructive call should still ask")
	}
}

func TestPermissionAudit_DenyNotAudited(t *testing.T) {
	// A deny-rule veto inside auto mode must NOT produce an audit
	// event: the denial already surfaces as an error tool_result and
	// through the PermissionDenied hook chain.
	perms := permissions.NewContext()
	perms.SetMode(permissions.ModeAuto)
	perms.AddRules(permissions.SrcUserSettings, permissions.BehaviorDeny,
		[]string{"Bash"})
	bash := &fakeTool{name: "Bash", destructive: true}
	eng := newAuditEngine(t, perms, bash, `{"command":"rm -rf /tmp/x"}`)

	done := make(chan []Event, 1)
	go func() { done <- drainAnswering(eng.Submit(context.Background(), "rm x"), PermAllow) }()
	var events []Event
	select {
	case events = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("engine hung")
	}

	if n := len(auditEvents(events)); n != 0 {
		t.Errorf("denied call must not audit (error tool_result covers it); got %d", n)
	}
	if bash.calls != 0 {
		t.Errorf("denied tool must not run; calls=%d", bash.calls)
	}
	if !hasEvent(events, func(r *ToolUseResultEvent) bool { return r.Result.IsError }) {
		t.Error("denial should surface as an error tool_result")
	}
}
