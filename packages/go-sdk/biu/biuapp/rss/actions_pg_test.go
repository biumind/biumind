package rss

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// withCaller injects a synthetic user claim — same shape api.Server's
// requireAuth would build from a Bearer token.
func withCaller(ctx context.Context, userID string) context.Context {
	return bauth.WithClaims(ctx, &bauth.Claims{UserID: userID})
}

func newPGApp(t *testing.T) *App {
	t.Helper()
	pool := openDB(t)
	return NewWithPool(pool)
}

func TestPGActions_AddListRemove(t *testing.T) {
	a := newPGApp(t)
	ctx := withCaller(context.Background(), "user-"+t.Name())

	// list empty
	out, err := a.Invoke(ctx, "feeds_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(map[string]any)["items"]; len(got.([]map[string]any)) != 0 {
		t.Errorf("expected empty list, got %v", got)
	}

	// add (no upstream — title falls back to URL)
	addOut, err := a.Invoke(ctx, "feeds_add", json.RawMessage(`{"url":"https://nonexistent.example/x.xml","title":"My Feed"}`))
	if err != nil {
		t.Fatal(err)
	}
	feedID := addOut.(map[string]any)["id"].(string)

	// list one
	out, _ = a.Invoke(ctx, "feeds_list", json.RawMessage(`{}`))
	items := out.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 || items[0]["title"] != "My Feed" {
		t.Errorf("list: %+v", items)
	}

	// remove
	if _, err := a.Invoke(ctx, "feeds_remove", json.RawMessage(`{"id":"`+feedID+`"}`)); err != nil {
		t.Fatal(err)
	}

	// list empty again
	out, _ = a.Invoke(ctx, "feeds_list", json.RawMessage(`{}`))
	if got := len(out.(map[string]any)["items"].([]map[string]any)); got != 0 {
		t.Errorf("after remove len = %d", got)
	}
}

func TestPGActions_FeedsAddDiscoversTitle(t *testing.T) {
	a := newPGApp(t)
	ctx := withCaller(context.Background(), "user-"+t.Name())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss version="2.0"><channel>
		  <title>Discovered Title</title><link>https://disc.example.com</link>
		  <item><title>p1</title><guid>p1</guid></item>
		</channel></rss>`))
	}))
	defer srv.Close()

	out, err := a.Invoke(ctx, "feeds_add", json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if title := out.(map[string]any)["title"]; title != "Discovered Title" {
		t.Errorf("title not auto-discovered: %v", title)
	}
	t.Cleanup(func() {
		id := out.(map[string]any)["id"].(string)
		_, _ = a.Invoke(ctx, "feeds_remove", json.RawMessage(`{"id":"`+id+`"}`))
	})
}

func TestPGActions_NoCallerRejects(t *testing.T) {
	a := newPGApp(t)
	ctx := context.Background() // no claims

	_, err := a.Invoke(ctx, "feeds_list", json.RawMessage(`{}`))
	if !errors.Is(err, ErrNoCaller) {
		t.Errorf("expected ErrNoCaller, got %v", err)
	}
}

func TestPGActions_EntriesListAndMarkRead(t *testing.T) {
	a := newPGApp(t)
	ctx := withCaller(context.Background(), "user-"+t.Name())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss version="2.0"><channel>
		  <title>x</title>
		  <item><title>e1</title><guid>e1</guid><link>https://x.com/1</link></item>
		  <item><title>e2</title><guid>e2</guid><link>https://x.com/2</link></item>
		</channel></rss>`))
	}))
	defer srv.Close()

	addOut, _ := a.Invoke(ctx, "feeds_add", json.RawMessage(`{"url":"`+srv.URL+`"}`))
	feedID := addOut.(map[string]any)["id"].(string)
	t.Cleanup(func() {
		_, _ = a.Invoke(ctx, "feeds_remove", json.RawMessage(`{"id":"`+feedID+`"}`))
	})

	if _, err := a.Invoke(ctx, "feeds_refresh", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}

	listOut, err := a.Invoke(ctx, "entries_list", json.RawMessage(`{"feed_id":"`+feedID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	entries := listOut.(map[string]any)["items"].([]map[string]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}

	cnt, _ := a.Invoke(ctx, "unread_count", nil)
	if c := cnt.(map[string]any)["count"].(int); c != 2 {
		t.Errorf("unread = %d, want 2", c)
	}

	first := entries[0]["id"].(string)
	if _, err := a.Invoke(ctx, "entries_mark_read", json.RawMessage(`{"id":"`+first+`"}`)); err != nil {
		t.Fatal(err)
	}
	cnt, _ = a.Invoke(ctx, "unread_count", nil)
	if c := cnt.(map[string]any)["count"].(int); c != 1 {
		t.Errorf("after mark, unread = %d, want 1", c)
	}
}

func TestPGActions_OldNamesDelegate(t *testing.T) {
	a := newPGApp(t)
	ctx := withCaller(context.Background(), "user-"+t.Name())

	// subscribe → feeds_add
	out, err := a.Invoke(ctx, "subscribe", json.RawMessage(`{"url":"https://e.com/old.xml","title":"Old"}`))
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]any)["id"].(string)
	if !strings.Contains(id, "-") {
		t.Errorf("id should be uuid, got %q", id)
	}
	t.Cleanup(func() {
		_, _ = a.Invoke(ctx, "feeds_remove", json.RawMessage(`{"id":"`+id+`"}`))
	})

	// list_subscriptions → feeds_list
	listOut, _ := a.Invoke(ctx, "list_subscriptions", json.RawMessage(`{}`))
	if items := listOut.(map[string]any)["items"].([]map[string]any); len(items) != 1 {
		t.Errorf("legacy list_subscriptions returned %d items", len(items))
	}
}
