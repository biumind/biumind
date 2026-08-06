package apptools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/runtime/internal/agent"
)

// stubApp implements biuapp.App with a recordable Invoke.
type stubApp struct {
	manifest biuapp.Manifest
	calls    []string
	failOn   string
}

func (s *stubApp) Manifest() biuapp.Manifest                     { return s.manifest }
func (s *stubApp) Init(ctx context.Context, _ biuapp.Deps) error { return nil }
func (s *stubApp) Invoke(_ context.Context, action string, in json.RawMessage) (any, error) {
	s.calls = append(s.calls, action+":"+string(in))
	if action == s.failOn {
		return nil, errors.New("forced fail")
	}
	return map[string]any{"action": action, "ok": true}, nil
}

func newStubApp(slug string, actions ...biuapp.ActionSpec) *stubApp {
	return &stubApp{
		manifest: biuapp.Manifest{
			Name:        slug,
			Version:     "0.1.0",
			Description: "stub for tests",
			Actions:     actions,
			ManifestExt: biuapp.ManifestExt{
				Identifier: slug,
				Title:      strings.ToUpper(slug),
			},
		},
	}
}

// ─── RegisterTools ──────────────────────────────────────────

func TestRegisterTools_BindsActions(t *testing.T) {
	app := newStubApp("rss",
		biuapp.ActionSpec{Name: "fetch", Description: "fetch feed", Risk: biuapp.RiskLow},
		biuapp.ActionSpec{Name: "subscribe", Description: "subscribe", Risk: biuapp.RiskMedium},
	)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(context.Background(), app); err != nil {
		t.Fatal(err)
	}

	loaded := &Loaded{
		Apps: []LoadedApp{{
			InstallID:        "i-1",
			Identifier:       "rss",
			Version:          "0.1.0",
			Manifest:         app.Manifest(),
			AvailableActions: app.Manifest().Actions,
		}},
	}
	rt := agent.NewRegistry()
	n := RegisterTools(rt, loaded, ToolDeps{Registry: reg})
	if n != 2 {
		t.Errorf("registered %d tools, want 2", n)
	}
	if _, ok := rt.Get("rss.fetch"); !ok {
		t.Error("rss.fetch not registered")
	}
	if _, ok := rt.Get("rss.subscribe"); !ok {
		t.Error("rss.subscribe not registered")
	}
}

func TestRegisterTools_RouteThroughBiuappRegistry(t *testing.T) {
	app := newStubApp("rss",
		biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow},
	)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)

	loaded := &Loaded{Apps: []LoadedApp{{
		InstallID: "i-1", Identifier: "rss", Manifest: app.Manifest(),
		AvailableActions: app.Manifest().Actions,
	}}}
	rt := agent.NewRegistry()
	RegisterTools(rt, loaded, ToolDeps{Registry: reg})

	tool, _ := rt.Get("rss.fetch")
	out, err := tool.Invoke(context.Background(), map[string]any{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out, `"ok":true`) {
		t.Errorf("output = %s", out)
	}
	if len(app.calls) != 1 {
		t.Errorf("App.Invoke called %d times, want 1: %+v", len(app.calls), app.calls)
	}
	if !strings.Contains(app.calls[0], `"url":"https://example.com"`) {
		t.Errorf("input not forwarded: %s", app.calls[0])
	}
}

func TestRegisterTools_RiskMapping(t *testing.T) {
	app := newStubApp("a",
		biuapp.ActionSpec{Name: "low", Risk: biuapp.RiskLow},
		biuapp.ActionSpec{Name: "med", Risk: biuapp.RiskMedium},
		biuapp.ActionSpec{Name: "hi", Risk: biuapp.RiskHigh},
		biuapp.ActionSpec{Name: "default" /* no risk set */},
	)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	rt := agent.NewRegistry()
	RegisterTools(rt, &Loaded{Apps: []LoadedApp{{
		Identifier: "a", Manifest: app.Manifest(),
		AvailableActions: app.Manifest().Actions,
	}}}, ToolDeps{Registry: reg})

	cases := map[string]agent.Risk{
		"a.low":     agent.RiskLow,
		"a.med":     agent.RiskMedium,
		"a.hi":      agent.RiskHigh,
		"a.default": agent.RiskMedium, // unset = medium (safe default)
	}
	for name, want := range cases {
		tool, _ := rt.Get(name)
		if tool == nil {
			t.Fatalf("%s not registered", name)
		}
		if tool.Risk != want {
			t.Errorf("%s risk = %v, want %v", name, tool.Risk, want)
		}
	}
}

// ─── Authz gating ───────────────────────────────────────────

type stubAuthz struct {
	deny bool
}

func (s stubAuthz) Check(_ context.Context, _, _ string, _, _ map[string]any) error {
	if s.deny {
		return errors.New("authz says no")
	}
	return nil
}

func TestRegisterTools_AuthzAllow(t *testing.T) {
	app := newStubApp("rss", biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow})
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)

	rt := agent.NewRegistry()
	RegisterTools(rt, &Loaded{Apps: []LoadedApp{{
		InstallID: "i-1", Identifier: "rss", Manifest: app.Manifest(),
		AvailableActions: app.Manifest().Actions,
	}}}, ToolDeps{Registry: reg, Authz: stubAuthz{deny: false}})

	tool, _ := rt.Get("rss.fetch")
	if _, err := tool.Invoke(context.Background(), nil); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
	if len(app.calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(app.calls))
	}
}

func TestRegisterTools_AuthzDeny(t *testing.T) {
	app := newStubApp("rss", biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow})
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)

	rt := agent.NewRegistry()
	RegisterTools(rt, &Loaded{Apps: []LoadedApp{{
		InstallID: "i-1", Identifier: "rss", Manifest: app.Manifest(),
		AvailableActions: app.Manifest().Actions,
	}}}, ToolDeps{Registry: reg, Authz: stubAuthz{deny: true}})

	tool, _ := rt.Get("rss.fetch")
	_, err := tool.Invoke(context.Background(), nil)
	if err == nil {
		t.Error("expected denial error")
	}
	if !strings.Contains(err.Error(), "authz") {
		t.Errorf("expected authz error, got %v", err)
	}
	if len(app.calls) != 0 {
		t.Errorf("App.Invoke should NOT have been called on deny, got %d", len(app.calls))
	}
}

// ─── Recorder integration ──────────────────────────────────

type captureRecorder struct {
	records []InvocationRecord
}

func (c *captureRecorder) Record(_ context.Context, inv InvocationRecord) error {
	c.records = append(c.records, inv)
	return nil
}

func TestRegisterTools_RecordsOk(t *testing.T) {
	app := newStubApp("rss", biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow})
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	rec := &captureRecorder{}

	rt := agent.NewRegistry()
	RegisterTools(rt, &Loaded{Apps: []LoadedApp{{
		InstallID: "i-1", Identifier: "rss", Manifest: app.Manifest(),
		AvailableActions: app.Manifest().Actions,
	}}}, ToolDeps{Registry: reg, Recorder: rec})

	tool, _ := rt.Get("rss.fetch")
	_, _ = tool.Invoke(context.Background(), nil)
	if len(rec.records) != 1 {
		t.Fatalf("got %d records, want 1", len(rec.records))
	}
	if rec.records[0].Status != "ok" {
		t.Errorf("status = %q", rec.records[0].Status)
	}
	if rec.records[0].Caller != "agent" {
		t.Errorf("caller = %q", rec.records[0].Caller)
	}
}

func TestRegisterTools_RecordsErrorOnAppFailure(t *testing.T) {
	app := newStubApp("rss", biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow})
	app.failOn = "fetch"
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	rec := &captureRecorder{}

	rt := agent.NewRegistry()
	RegisterTools(rt, &Loaded{Apps: []LoadedApp{{
		InstallID: "i-1", Identifier: "rss", Manifest: app.Manifest(),
		AvailableActions: app.Manifest().Actions,
	}}}, ToolDeps{Registry: reg, Recorder: rec})

	tool, _ := rt.Get("rss.fetch")
	_, err := tool.Invoke(context.Background(), nil)
	if err == nil {
		t.Fatal("expected app failure to propagate")
	}
	if rec.records[0].Status != "error" {
		t.Errorf("status = %q, want error", rec.records[0].Status)
	}
}

// ─── Prompt block ──────────────────────────────────────────

func TestBuildSystemPromptBlock_Empty(t *testing.T) {
	if got := BuildSystemPromptBlock(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
	if got := BuildSystemPromptBlock(&Loaded{}); got != "" {
		t.Errorf("expected empty for no apps, got %q", got)
	}
}

func TestBuildSystemPromptBlock_ListsApps(t *testing.T) {
	loaded := &Loaded{Apps: []LoadedApp{
		{
			Identifier: "rss",
			Manifest: biuapp.Manifest{
				Name: "rss", Description: "RSS feeds",
				ManifestExt: biuapp.ManifestExt{Title: "RSS 订阅"},
			},
			AvailableActions: []biuapp.ActionSpec{{Name: "fetch"}, {Name: "subscribe"}},
		},
		{
			Identifier: "email",
			Manifest: biuapp.Manifest{
				Name: "email", Description: "Email management",
				ManifestExt: biuapp.ManifestExt{Title: "邮件管理"},
			},
			AvailableActions: []biuapp.ActionSpec{{Name: "list_inbox"}, {Name: "draft"}},
		},
	}}

	block := BuildSystemPromptBlock(loaded)
	for _, want := range []string{
		"## Available Apps",
		"**rss**",
		"(RSS 订阅)",
		"RSS feeds",
		"rss.fetch, rss.subscribe",
		"**email**",
		"email.list_inbox, email.draft",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q\n--- block ---\n%s", want, block)
		}
	}
}

func TestBuildSystemPromptBlock_OrderingDeterministic(t *testing.T) {
	loaded := &Loaded{Apps: []LoadedApp{
		{Identifier: "zeta", Manifest: biuapp.Manifest{Description: "Z"}},
		{Identifier: "alpha", Manifest: biuapp.Manifest{Description: "A"}},
		{Identifier: "mu", Manifest: biuapp.Manifest{Description: "M"}},
	}}
	block := BuildSystemPromptBlock(loaded)
	idxA := strings.Index(block, "**alpha**")
	idxM := strings.Index(block, "**mu**")
	idxZ := strings.Index(block, "**zeta**")
	if idxA < 0 || idxM < 0 || idxZ < 0 {
		t.Fatal("missing entries")
	}
	if !(idxA < idxM && idxM < idxZ) {
		t.Errorf("ordering wrong: alpha@%d mu@%d zeta@%d", idxA, idxM, idxZ)
	}
}
