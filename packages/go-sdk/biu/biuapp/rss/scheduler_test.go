package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestScheduler_RefreshAll_EndToEnd(t *testing.T) {
	pool := openDB(t)
	store := NewPGStore(pool)
	ctx := context.Background()

	scope, scopeID := freshScope()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`<rss version="2.0"><channel>
		  <title>Sched Test</title>
		  <link>https://sched.example.com</link>
		  <item><title>p1</title><link>https://sched.example.com/p1</link><guid>p1</guid></item>
		  <item><title>p2</title><link>https://sched.example.com/p2</link><guid>p2</guid></item>
		</channel></rss>`))
	}))
	defer srv.Close()

	f, err := store.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: scopeID, FeedURL: srv.URL, Title: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.RemoveFeed(ctx, scope, scopeID, f.ID) })

	sched := NewScheduler(store, NewDefaultFetcher())
	stats, err := sched.RefreshAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.OK != 1 || stats.NewEntries != 2 {
		t.Errorf("first: %+v, want OK=1 NewEntries=2", stats)
	}

	got, _ := store.GetFeed(ctx, f.ID)
	if got.Etag != `"v1"` {
		t.Errorf("etag = %q", got.Etag)
	}
	if got.LastStatus != "ok" {
		t.Errorf("status = %q", got.LastStatus)
	}
	if got.Title != "Sched Test" {
		t.Errorf("title = %q", got.Title)
	}

	// Second tick — feed not yet due (default 1800s), so RefreshAll
	// should consider 0.
	stats2, err := sched.RefreshAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Considered != 0 {
		t.Errorf("second tick should skip non-due feeds, got %+v", stats2)
	}
}

func TestScheduler_RefreshAll_HandlesError(t *testing.T) {
	pool := openDB(t)
	store := NewPGStore(pool)
	ctx := context.Background()

	scope, scopeID := freshScope()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f, _ := store.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: scopeID, FeedURL: srv.URL, Title: "bad",
	})
	t.Cleanup(func() { _ = store.RemoveFeed(ctx, scope, scopeID, f.ID) })

	sched := NewScheduler(store, NewDefaultFetcher())
	stats, _ := sched.RefreshAll(ctx)
	if stats.Errors != 1 {
		t.Errorf("expected 1 error, got %+v", stats)
	}
	got, _ := store.GetFeed(ctx, f.ID)
	if got.LastStatus != "error" {
		t.Errorf("status = %q", got.LastStatus)
	}
	if got.ConsecutiveFailures != 1 {
		t.Errorf("failures = %d", got.ConsecutiveFailures)
	}
}
