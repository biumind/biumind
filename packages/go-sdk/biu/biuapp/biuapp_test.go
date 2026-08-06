package biuapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeApp — tiny test app that records invocations.
type fakeApp struct {
	manifest Manifest
	calls    []string
}

func (f *fakeApp) Manifest() Manifest                        { return f.manifest }
func (f *fakeApp) Init(ctx context.Context, deps Deps) error { return nil }
func (f *fakeApp) Invoke(ctx context.Context, action string, in json.RawMessage) (any, error) {
	f.calls = append(f.calls, action+":"+string(in))
	if action == "fail" {
		return nil, errors.New("forced failure")
	}
	return map[string]any{"ok": true, "echo": json.RawMessage(in)}, nil
}

func TestRegistryRegisterAndList(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &fakeApp{manifest: Manifest{
		Name: "a", Version: "0.1.0",
		Actions: []ActionSpec{{Name: "ping"}},
	}}
	b := &fakeApp{manifest: Manifest{
		Name: "b", Version: "0.1.0",
		Actions: []ActionSpec{{Name: "ping"}},
	}}
	if err := r.Register(context.Background(), a); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(context.Background(), b); err != nil {
		t.Fatalf("register b: %v", err)
	}
	list := r.List()
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Errorf("list order wrong: %+v", list)
	}
}

func TestRegistryRejectsDuplicateName(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &fakeApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "ping"}}}}
	a2 := &fakeApp{manifest: Manifest{Name: "x", Actions: []ActionSpec{{Name: "ping"}}}}
	if err := r.Register(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(context.Background(), a2); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
}

func TestRegistryInvokeRoutes(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &fakeApp{manifest: Manifest{
		Name:    "a",
		Actions: []ActionSpec{{Name: "ping"}, {Name: "fail"}},
	}}
	_ = r.Register(context.Background(), a)

	out, err := r.Invoke(context.Background(), "a", "ping", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["ok"] != true {
		t.Errorf("bad result: %+v", out)
	}
	if len(a.calls) != 1 || a.calls[0] != `ping:{"x":1}` {
		t.Errorf("calls = %+v", a.calls)
	}
}

func TestRegistryInvokeUnknownAppOrAction(t *testing.T) {
	r := NewRegistry(Deps{})
	a := &fakeApp{manifest: Manifest{Name: "a", Actions: []ActionSpec{{Name: "ping"}}}}
	_ = r.Register(context.Background(), a)

	if _, err := r.Invoke(context.Background(), "missing", "ping", nil); err == nil ||
		!strings.Contains(err.Error(), "unknown app") {
		t.Errorf("want unknown-app error, got %v", err)
	}
	if _, err := r.Invoke(context.Background(), "a", "missing", nil); err == nil ||
		!strings.Contains(err.Error(), "no action") {
		t.Errorf("want no-action error, got %v", err)
	}
}

func TestManifestSortIsDeterministic(t *testing.T) {
	r := NewRegistry(Deps{})
	for _, name := range []string{"zeta", "alpha", "mu"} {
		_ = r.Register(context.Background(), &fakeApp{
			manifest: Manifest{Name: name, Actions: []ActionSpec{{Name: "x"}}},
		})
	}
	got := []string{}
	for _, m := range r.List() {
		got = append(got, m.Name)
	}
	want := []string{"alpha", "mu", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
