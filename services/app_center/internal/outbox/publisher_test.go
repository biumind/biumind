package outbox

import "testing"

func TestTopicForScope_KnownKinds(t *testing.T) {
	cases := []struct {
		scope string
		want  string
	}{
		{"install:abc-123", "app:install:abc-123"},
		{"app:app_rss", "app:catalog:app_rss"},
		{"user:00000000-0000-0000-0000-000000000001", "sidebar:user:00000000-0000-0000-0000-000000000001"},
		{"org:org_acme", "app:org:org_acme"},
	}
	for _, c := range cases {
		t.Run(c.scope, func(t *testing.T) {
			if got := TopicForScope(c.scope); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestTopicForScope_UnknownKindsRoutedSafely(t *testing.T) {
	// Unknown but well-formed scope → app:unknown:<full>; the row
	// reaches no subscriber but won't loop forever.
	got := TopicForScope("future:xyz")
	want := "app:unknown:future:xyz"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// Malformed (no colon) → also caught by the unknown bucket.
	got2 := TopicForScope("bare-token")
	want2 := "app:unknown:bare-token"
	if got2 != want2 {
		t.Errorf("got %q want %q", got2, want2)
	}
}

func TestKindFor_KnownEvents(t *testing.T) {
	cases := map[string]string{
		"app.installed":           "biumind.app.installed",
		"app.uninstalled":         "biumind.app.uninstalled",
		"app.action_invoked":      "biumind.app.action_invoked",
		"app.view_data_changed":   "biumind.app.view_data_changed",
		"app.trigger_fired":       "biumind.app.trigger_fired",
		"app.permissions_changed": "biumind.app.permissions_changed",
		"sidebar.layout_changed":  "biumind.sidebar.layout_changed",
	}
	for in, want := range cases {
		if got := KindFor(in); got != want {
			t.Errorf("KindFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKindFor_UnknownDerivesFromPrefix(t *testing.T) {
	// Forward-compat: a future event_type still flows through with a
	// derived kind so the row doesn't get stuck.
	got := KindFor("app.something_new")
	want := "biumind.app.something_new"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNoop_AcceptsAnything(t *testing.T) {
	if err := (Noop{}).Publish(t.Context(), "any", "any", nil); err != nil {
		t.Errorf("Noop must not error: %v", err)
	}
}

func TestMemory_CapturesPublishes(t *testing.T) {
	m := &Memory{}
	_ = m.Publish(t.Context(), "topic-a", "kind-a", map[string]any{"x": 1})
	_ = m.Publish(t.Context(), "topic-b", "kind-b", map[string]any{"y": 2})
	if len(m.Events) != 2 {
		t.Fatalf("events=%d", len(m.Events))
	}
	if m.Events[0].Topic != "topic-a" || m.Events[1].Kind != "kind-b" {
		t.Errorf("captured wrong: %+v", m.Events)
	}
}

func TestRealtime_NilURLNoOp(t *testing.T) {
	r := NewRealtime("", nil)
	if err := r.Publish(t.Context(), "t", "k", nil); err != nil {
		t.Errorf("empty URL should silently succeed, got %v", err)
	}
}
