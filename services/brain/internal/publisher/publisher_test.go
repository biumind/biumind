package publisher

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRealtimePublishOK(t *testing.T) {
	var captured []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	p := NewRealtime(ts.URL, nopLogger())
	if err := p.Publish(context.Background(), "wiki:project:p1", "page.created", map[string]any{
		"page_id": "x", "title": "Hello",
	}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatal(err)
	}
	if got["topic"] != "wiki:project:p1" || got["kind"] != "page.created" {
		t.Errorf("body = %s", string(captured))
	}
}

func TestRealtimeEmptyURLNoop(t *testing.T) {
	if err := NewRealtime("", nopLogger()).Publish(context.Background(), "t", "k", nil); err != nil {
		t.Fatalf("expected no-op success; got %v", err)
	}
}

func TestMemoryCapture(t *testing.T) {
	m := &Memory{}
	_ = m.Publish(context.Background(), "t1", "kind1", map[string]any{"x": 1})
	_ = m.Publish(context.Background(), "t2", "kind2", nil)
	if len(m.Events) != 2 {
		t.Fatalf("got %d events", len(m.Events))
	}
	if m.Events[0].Topic != "t1" || m.Events[0].Kind != "kind1" {
		t.Errorf("events[0] = %+v", m.Events[0])
	}
}
