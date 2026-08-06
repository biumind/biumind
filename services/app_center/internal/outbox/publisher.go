// Package outbox — Realtime publisher + poller for app_center.events.
//
// Mirrors services/brain/internal/publisher + events shape. Kept in its
// own package so the dependency graph stays one-directional:
//
//	cmd/app_center → outbox → events (read-only of constants)
//
// Both directions of the App Center event schema (publish → topic name,
// scope → routing) live here so the install / sidebar / triggers
// packages don't have to know about Realtime at all.

package outbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Publisher is the slice of services/realtime our poller reaches.
// Defined as an interface so tests can capture publish calls without
// wiring an actual Realtime server.
type Publisher interface {
	Publish(ctx context.Context, topic, kind string, payload map[string]any) error
}

// Realtime is the production Publisher: HTTP POST to the Realtime
// service's internal publish endpoint.
type Realtime struct {
	URL    string
	HC     *http.Client
	Logger *slog.Logger
}

func NewRealtime(url string, logger *slog.Logger) *Realtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Realtime{URL: url, HC: &http.Client{Timeout: 3 * time.Second}, Logger: logger}
}

// Publish writes one event to Realtime. Empty URL → no-op (dev mode
// without a Realtime service running).
func (r *Realtime) Publish(ctx context.Context, topic, kind string, payload map[string]any) error {
	if r == nil || r.URL == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"topic": topic, "kind": kind, "payload": payload,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.URL+"/v1/internal/publish", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.HC.Do(req)
	if err != nil {
		return fmt.Errorf("outbox.Realtime: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("outbox.Realtime: status %d", resp.StatusCode)
	}
	return nil
}

// Noop swallows publishes — used when the Realtime URL is empty so
// the poller can run end-to-end in CI without a Realtime service.
type Noop struct{}

func (Noop) Publish(_ context.Context, _, _ string, _ map[string]any) error { return nil }

// Memory captures publishes into a slice. Tests assert against this
// to verify scope → topic mapping without standing up Realtime.
type Memory struct {
	Events []Captured
}

type Captured struct {
	Topic   string
	Kind    string
	Payload map[string]any
}

func (m *Memory) Publish(_ context.Context, topic, kind string, payload map[string]any) error {
	m.Events = append(m.Events, Captured{Topic: topic, Kind: kind, Payload: payload})
	return nil
}

// ─── Scope → Realtime topic translation ──────────────────────────
//
// Decision: app_center events live under one of these scope kinds,
// each mapping to a distinct Realtime topic. The scope→topic table
// stays in this package so all readers find one source of truth.
//
//	scope                    topic                          consumer
//	install:<install_id> →   app:install:<install_id>       AppViewHost (refresh_on)
//	app:<app_id>         →   app:catalog:<app_id>           App Center home (badge dot)
//	user:<user_id>       →   sidebar:user:<user_id>         Sidebar customize page
//	org:<org_id>         →   app:org:<org_id>               Org admin dashboard
//
// Unknown scope kinds become "app:unknown:<scope>" — reaches no
// subscriber but keeps the row from looping forever.
func TopicForScope(scope string) string {
	// scope is "<kind>:<id>"; split on the first colon.
	for i := 0; i < len(scope); i++ {
		if scope[i] == ':' {
			kind, id := scope[:i], scope[i+1:]
			switch kind {
			case "install":
				return "app:install:" + id
			case "app":
				return "app:catalog:" + id
			case "user":
				return "sidebar:user:" + id
			case "org":
				return "app:org:" + id
			}
		}
	}
	return "app:unknown:" + scope
}

// EventKind maps an app_center event_type to the AG-UI CUSTOM event
// name the client switches on. Mirrors the registry in
// packages/proto/biumind/agui/v1/agui.proto §App Center.
var eventKindMap = map[string]string{
	"app.installed":           "biumind.app.installed",
	"app.uninstalled":         "biumind.app.uninstalled",
	"app.upgraded":            "biumind.app.upgraded",
	"app.permissions_changed": "biumind.app.permissions_changed",
	"app.action_invoked":      "biumind.app.action_invoked",
	"app.trigger_fired":       "biumind.app.trigger_fired",
	"app.view_data_changed":   "biumind.app.view_data_changed",
	"app.published":           "biumind.app.published",
	"app.deprecated":          "biumind.app.deprecated",
	"app.suspended":           "biumind.app.suspended",
	"app.enabled_changed":     "biumind.app.enabled_changed",
	"app.config_updated":      "biumind.app.config_updated",
	"sidebar.layout_changed":  "biumind.sidebar.layout_changed",
}

func KindFor(eventType string) string {
	if k, ok := eventKindMap[eventType]; ok {
		return k
	}
	// Forward-compat: unknown event types still flow through with a
	// derived kind so a new event type doesn't disappear because the
	// poller wasn't updated. Subscribers ignore unknown kinds.
	return "biumind." + eventType
}
