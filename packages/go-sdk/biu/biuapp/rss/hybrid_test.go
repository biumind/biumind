// hybrid_test.go — covers the v1.5 actions added when RSS upgraded
// from backend-only to hybrid form. The original fetch action and
// XML parser keep their existing tests in rss_test.go.

package rss

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
)

func sampleSrv(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ─── manifest assertions ──────────────────────────────────────────

func TestManifest_DeclaresHybrid(t *testing.T) {
	a := New()
	m := a.Manifest()
	if m.Kind != "hybrid" {
		t.Errorf("Kind = %q, want hybrid", m.Kind)
	}
	if m.Identifier != "rss" {
		t.Errorf("Identifier = %q", m.Identifier)
	}
	if m.Title == "" {
		t.Error("Title (display name) should be set")
	}
	if got := m.Slug(); got != "rss" {
		t.Errorf("Slug() = %q", got)
	}
}

func TestManifest_HasExpectedActions(t *testing.T) {
	m := New().Manifest()
	want := []string{
		"subscribe", "unsubscribe", "list_subscriptions",
		"fetch", "refresh_all", "digest",
	}
	got := map[string]bool{}
	for _, a := range m.Actions {
		got[a.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing action %q", w)
		}
	}
}

func TestManifest_HasExpectedViews(t *testing.T) {
	m := New().Manifest()
	wantViews := map[string]biuapp.ViewLayout{
		"home": biuapp.LayoutListDetail,
		"add":  biuapp.LayoutForm,
	}
	got := map[string]biuapp.ViewLayout{}
	for _, v := range m.Views {
		got[v.ID] = v.Layout
	}
	for id, layout := range wantViews {
		if got[id] != layout {
			t.Errorf("view %q layout = %v, want %v", id, got[id], layout)
		}
	}
}

func TestManifest_HasCronTriggers(t *testing.T) {
	m := New().Manifest()
	if len(m.Triggers) < 2 {
		t.Fatalf("want ≥ 2 triggers, got %d", len(m.Triggers))
	}
	for _, tr := range m.Triggers {
		if tr.Kind != biuapp.TriggerCron {
			t.Errorf("trigger %q is not cron: %v", tr.Name, tr.Kind)
		}
		if tr.Expr == "" || tr.Action == "" {
			t.Errorf("trigger %q missing expr/action", tr.Name)
		}
	}
}

func TestManifest_PassesValidation(t *testing.T) {
	m := New().Manifest()
	if err := biuapp.ValidateBundled(&m); err != nil {
		t.Errorf("manifest must validate: %v", err)
	}
}

// ─── action tests ─────────────────────────────────────────────────

func TestSubscribe_RecordsAndIsIdempotent(t *testing.T) {
	srv := sampleSrv(t)
	a := New()
	body, err := a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	first := body.(map[string]any)
	if first["subscription_id"] == "" {
		t.Errorf("missing subscription_id")
	}
	// Re-subscribe — same id (idempotent).
	body2, _ := a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`"}`))
	second := body2.(map[string]any)
	if first["subscription_id"] != second["subscription_id"] {
		t.Errorf("non-idempotent subscribe: %v vs %v",
			first["subscription_id"], second["subscription_id"])
	}
}

func TestSubscribe_ResolvesTitleFromFeedWhenAbsent(t *testing.T) {
	srv := sampleSrv(t)
	a := New()
	out, _ := a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if out.(map[string]any)["title"] != "Demo Feed" {
		t.Errorf("title should default to feed <title>, got %v", out)
	}
}

func TestSubscribe_KeepsExplicitTitle(t *testing.T) {
	srv := sampleSrv(t)
	a := New()
	out, _ := a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`","title":"My Feed"}`))
	if out.(map[string]any)["title"] != "My Feed" {
		t.Errorf("explicit title should win, got %v", out)
	}
}

func TestSubscribe_RejectsEmptyURL(t *testing.T) {
	if _, err := New().Invoke(context.Background(), "subscribe", json.RawMessage(`{}`)); err == nil {
		t.Error("expected error on empty url")
	}
}

func TestListSubscriptions_ReturnsItems(t *testing.T) {
	srv := sampleSrv(t)
	a := New()
	_, _ = a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`","title":"Feed A"}`))
	out, err := a.Invoke(context.Background(), "list_subscriptions", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	items, ok := out.(map[string]any)["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 item, got %v", out)
	}
	if items[0]["title"] != "Feed A" {
		t.Errorf("item title = %v", items[0]["title"])
	}
}

func TestUnsubscribe_RemovesEntry(t *testing.T) {
	srv := sampleSrv(t)
	a := New()
	out, _ := a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`"}`))
	id := out.(map[string]any)["subscription_id"].(string)

	_, err := a.Invoke(context.Background(), "unsubscribe",
		json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	listOut, _ := a.Invoke(context.Background(), "list_subscriptions", json.RawMessage(`{}`))
	items := listOut.(map[string]any)["items"].([]map[string]any)
	if len(items) != 0 {
		t.Errorf("expected empty after unsubscribe, got %d", len(items))
	}
}

func TestUnsubscribe_UnknownIDIs404(t *testing.T) {
	if _, err := New().Invoke(context.Background(), "unsubscribe",
		json.RawMessage(`{"id":"sub_doesnotexist"}`)); err == nil {
		t.Error("expected not-found error")
	}
}

func TestRefreshAll_StampsLastFetch(t *testing.T) {
	srv := sampleSrv(t)
	a := New()
	_, _ = a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`"}`))
	out, err := a.Invoke(context.Background(), "refresh_all", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	r := out.(map[string]any)
	if r["refreshed"].(int) != 1 {
		t.Errorf("refreshed = %v, want 1", r["refreshed"])
	}
}

func TestDigest_AssemblesItems(t *testing.T) {
	srv := sampleSrv(t)
	a := New()
	_, _ = a.Invoke(context.Background(), "subscribe",
		json.RawMessage(`{"url":"`+srv.URL+`"}`))
	out, err := a.Invoke(context.Background(), "digest",
		json.RawMessage(`{"window":"30d","max_items":5}`))
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	r := out.(map[string]any)
	if r["source_count"].(int) != 1 {
		t.Errorf("source_count = %v", r["source_count"])
	}
}

func TestDigest_AcceptsDayWindow(t *testing.T) {
	a := New()
	if _, err := a.Invoke(context.Background(), "digest",
		json.RawMessage(`{"window":"7d"}`)); err != nil {
		t.Errorf("7d window should parse: %v", err)
	}
}

func TestInvoke_UnknownActionWrapsErr(t *testing.T) {
	_, err := New().Invoke(context.Background(), "nonsense", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("want unknown-action err, got %v", err)
	}
}
