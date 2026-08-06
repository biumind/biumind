package biuapp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// hookedApp implements both App and LifecycleHooks + TriggerHandler so
// the dispatcher tests can verify routing.
type hookedApp struct {
	manifest      Manifest
	installed     int
	uninstalled   int
	upgraded      int
	configUpdated int
	triggered     int
	upgradeFrom   string
	triggerEv     *TriggerEvent
	failOn        string // method name to return error from
}

func (h *hookedApp) Manifest() Manifest                        { return h.manifest }
func (h *hookedApp) Init(ctx context.Context, deps Deps) error { return nil }
func (h *hookedApp) Invoke(ctx context.Context, action string, in json.RawMessage) (any, error) {
	if action == "fail" {
		return nil, errors.New("invoke fail")
	}
	return map[string]any{"ok": true}, nil
}

func (h *hookedApp) OnInstall(ctx context.Context, in Install) error {
	h.installed++
	if h.failOn == "OnInstall" {
		return errors.New("install fail")
	}
	return nil
}
func (h *hookedApp) OnUninstall(ctx context.Context, in Install) error {
	h.uninstalled++
	if h.failOn == "OnUninstall" {
		return errors.New("uninstall fail")
	}
	return nil
}
func (h *hookedApp) OnUpgrade(ctx context.Context, in Install, fromVersion string) error {
	h.upgraded++
	h.upgradeFrom = fromVersion
	return nil
}
func (h *hookedApp) OnConfigUpdate(ctx context.Context, in Install) error {
	h.configUpdated++
	return nil
}
func (h *hookedApp) OnTrigger(ctx context.Context, ev TriggerEvent) error {
	h.triggered++
	h.triggerEv = &ev
	return nil
}

func TestDispatchOnInstall_CallsHook(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &hookedApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "ping"}}}}
	if err := r.Register(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := r.DispatchOnInstall(context.Background(), "x", Install{ID: "i-1"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if a.installed != 1 {
		t.Errorf("installed = %d, want 1", a.installed)
	}
}

func TestDispatchOnInstall_NoHookIsNoop(t *testing.T) {
	// fakeApp from biuapp_test.go does NOT implement LifecycleHooks.
	r := NewRegistry(Deps{})
	a := &fakeApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "ping"}}}}
	_ = r.Register(context.Background(), a)
	if err := r.DispatchOnInstall(context.Background(), "x", Install{}); err != nil {
		t.Errorf("expected silent ok, got %v", err)
	}
}

func TestDispatchOnInstall_PropagatesError(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &hookedApp{
		manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "ping"}}},
		failOn:   "OnInstall",
	}
	_ = r.Register(context.Background(), a)
	err := r.DispatchOnInstall(context.Background(), "x", Install{ID: "i-1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDispatchOnUninstall_TolerantToMissingApp(t *testing.T) {
	r := NewRegistry(Deps{})
	// No app registered; the dispatcher should silently skip rather
	// than fail — the install row may have outlived an org-private app
	// that was deregistered.
	if err := r.DispatchOnUninstall(context.Background(), "ghost", Install{}); err != nil {
		t.Errorf("expected silent ok for unknown app, got %v", err)
	}
}

func TestDispatchOnUpgrade_PassesFromVersion(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &hookedApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "ping"}}}}
	_ = r.Register(context.Background(), a)
	if err := r.DispatchOnUpgrade(context.Background(), "x",
		Install{ID: "i-1", Version: "0.2.0"}, "0.1.5"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if a.upgraded != 1 {
		t.Errorf("upgraded = %d, want 1", a.upgraded)
	}
	if a.upgradeFrom != "0.1.5" {
		t.Errorf("upgradeFrom = %q, want %q", a.upgradeFrom, "0.1.5")
	}
}

func TestDispatchOnTrigger_HookedAppRoute(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &hookedApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "tick"}}}}
	_ = r.Register(context.Background(), a)
	ev := TriggerEvent{Name: "hourly", Action: "tick", Input: json.RawMessage(`{}`)}
	if err := r.DispatchOnTrigger(context.Background(), "x", ev); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if a.triggered != 1 {
		t.Errorf("triggered = %d, want 1", a.triggered)
	}
	if a.triggerEv == nil || a.triggerEv.Name != "hourly" {
		t.Errorf("trigger event not captured: %+v", a.triggerEv)
	}
}

func TestDispatchOnTrigger_DefaultRoutingForPlainApp(t *testing.T) {
	// fakeApp doesn't implement TriggerHandler; the dispatcher should
	// fall back to invoking the action directly.
	r := NewRegistry(Deps{})
	a := &fakeApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "tick"}}}}
	_ = r.Register(context.Background(), a)
	ev := TriggerEvent{Name: "hourly", Action: "tick", Input: json.RawMessage(`{}`)}
	if err := r.DispatchOnTrigger(context.Background(), "x", ev); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(a.calls) != 1 || a.calls[0] != `tick:{}` {
		t.Errorf("expected default routing to invoke tick, got %+v", a.calls)
	}
}

func TestDispatchOnTrigger_DefaultRoutingRejectsUnknownAction(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &fakeApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "tick"}}}}
	_ = r.Register(context.Background(), a)
	ev := TriggerEvent{Name: "hourly", Action: "missing", Input: json.RawMessage(`{}`)}
	if err := r.DispatchOnTrigger(context.Background(), "x", ev); err == nil {
		t.Error("expected error for unknown trigger action under default routing")
	}
}
