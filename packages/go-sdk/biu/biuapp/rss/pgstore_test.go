package rss

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests against rss.* schema. Skip when DATABASE_URL is
// unset; CI matrix runs the integration leg after `task migrate up`.
// Same pattern as services/app_center/internal/installs/installer_test.go.

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping rss pgstore integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"rss.feeds", "rss.entries"} {
		var exists bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`, table).Scan(&exists); err != nil {
			t.Fatalf("table check %s: %v", table, err)
		}
		if !exists {
			t.Skipf("%s missing; apply services/app_center/migrations", table)
		}
	}
	return pool
}

func freshScope() (string, string) { return "user", uuid.NewString() }

func TestPGStore_AddListGetFeed(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()

	f, err := s.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: id,
		FeedURL: "https://example.com/test1.xml",
		Title:   "Test 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.RemoveFeed(ctx, scope, id, f.ID) })

	if f.RefreshSec != 1800 {
		t.Errorf("default refresh_sec = %d", f.RefreshSec)
	}
	if !f.Enabled {
		t.Error("expected enabled=true")
	}

	// idempotency: re-adding same URL returns ErrFeedExists
	_, err = s.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: id,
		FeedURL: "https://example.com/test1.xml",
		Title:   "Test 1",
	})
	if !errors.Is(err, ErrFeedExists) {
		t.Errorf("expected ErrFeedExists, got %v", err)
	}

	got, err := s.GetFeed(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FeedURL != f.FeedURL {
		t.Errorf("get returned %+v", got)
	}

	feeds, err := s.ListFeeds(ctx, scope, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 {
		t.Errorf("list = %d, want 1", len(feeds))
	}
}

func TestPGStore_RemoveFeedCascadesEntries(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()

	f, err := s.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: id, FeedURL: "https://e.com/cascade.xml", Title: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertEntries(ctx, f.ID, []ParsedEntry{
		{GUID: "g1", Title: "t1", URL: "u1", TitleHash: titleHash("t1", "u1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFeed(ctx, scope, id, f.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM rss.entries WHERE feed_id=$1`, f.ID).Scan(&n)
	if n != 0 {
		t.Errorf("cascade left %d entries", n)
	}
}

func TestPGStore_InsertEntries_Dedup(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()
	f, _ := s.AddFeed(ctx, AddFeedInput{Scope: scope, ScopeID: id, FeedURL: "https://e.com/dedup.xml", Title: "x"})
	t.Cleanup(func() { _ = s.RemoveFeed(ctx, scope, id, f.ID) })

	in := []ParsedEntry{
		{GUID: "g1", Title: "t1", TitleHash: titleHash("t1", "")},
		{GUID: "g2", Title: "t2", TitleHash: titleHash("t2", "")},
	}
	n1, err := s.InsertEntries(ctx, f.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(n1) != 2 {
		t.Errorf("first insert = %d, want 2", len(n1))
	}
	n2, err := s.InsertEntries(ctx, f.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(n2) != 0 {
		t.Errorf("dup insert = %d, want 0", len(n2))
	}
	in3 := append(in, ParsedEntry{GUID: "g3", Title: "t3", TitleHash: titleHash("t3", "")})
	n3, err := s.InsertEntries(ctx, f.ID, in3)
	if err != nil {
		t.Fatal(err)
	}
	if len(n3) != 1 {
		t.Errorf("partial dup insert = %d, want 1", len(n3))
	}
}

func TestPGStore_MarkRead_UnreadCount(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()
	f, _ := s.AddFeed(ctx, AddFeedInput{Scope: scope, ScopeID: id, FeedURL: "https://e.com/read.xml", Title: "x"})
	t.Cleanup(func() { _ = s.RemoveFeed(ctx, scope, id, f.ID) })

	_, err := s.InsertEntries(ctx, f.ID, []ParsedEntry{
		{GUID: "g1", Title: "t1", TitleHash: titleHash("t1", "")},
		{GUID: "g2", Title: "t2", TitleHash: titleHash("t2", "")},
		{GUID: "g3", Title: "t3", TitleHash: titleHash("t3", "")},
	})
	if err != nil {
		t.Fatal(err)
	}

	n, _ := s.UnreadCount(ctx, scope, id)
	if n != 3 {
		t.Errorf("unread = %d, want 3", n)
	}

	entries, _ := s.ListEntries(ctx, f.ID, ListEntriesOpts{})
	if err := s.MarkRead(ctx, entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	n, _ = s.UnreadCount(ctx, scope, id)
	if n != 2 {
		t.Errorf("after mark = %d, want 2", n)
	}

	if err := s.MarkRead(ctx, entries[0].ID, false); err != nil {
		t.Fatal(err)
	}
	n, _ = s.UnreadCount(ctx, scope, id)
	if n != 3 {
		t.Errorf("after unread = %d, want 3", n)
	}
}

func TestPGStore_DueFeeds(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()
	f, _ := s.AddFeed(ctx, AddFeedInput{Scope: scope, ScopeID: id, FeedURL: "https://e.com/due.xml", Title: "x"})
	t.Cleanup(func() { _ = s.RemoveFeed(ctx, scope, id, f.ID) })

	due, err := s.DueFeeds(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range due {
		if d.ID == f.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("never-fetched feed should be due, got %d feeds", len(due))
	}

	// fetch state
	if err := s.UpdateFetchState(ctx, f.ID, FetchOutcome{Status: "ok", Etag: `"abc"`}); err != nil {
		t.Fatal(err)
	}
	due2, _ := s.DueFeeds(ctx, 100)
	for _, d := range due2 {
		if d.ID == f.ID {
			t.Errorf("just-fetched feed should not be due")
		}
	}
}

func TestPGStore_UpdateFetchState_PreservesOnEmptyFields(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()
	f, _ := s.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: id, FeedURL: "https://e.com/preserve.xml", Title: "Original",
	})
	t.Cleanup(func() { _ = s.RemoveFeed(ctx, scope, id, f.ID) })

	// First update writes etag + title
	if err := s.UpdateFetchState(ctx, f.ID, FetchOutcome{
		Status: "ok", Etag: `"v1"`, Title: "Real Title", IconURL: "https://e.com/i.png",
	}); err != nil {
		t.Fatal(err)
	}

	// Subsequent error update — empty etag/title must not wipe existing
	if err := s.UpdateFetchState(ctx, f.ID, FetchOutcome{
		Status: "error", ErrMsg: "boom",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFeed(ctx, f.ID)
	if got.Etag != `"v1"` {
		t.Errorf("etag wiped: %q", got.Etag)
	}
	if got.Title != "Real Title" {
		t.Errorf("title wiped: %q", got.Title)
	}
	if got.LastError != "boom" {
		t.Errorf("err msg = %q", got.LastError)
	}
	if got.ConsecutiveFailures != 1 {
		t.Errorf("failures = %d", got.ConsecutiveFailures)
	}
}

// regression: time.Time zero handling — never-fetched feed scans cleanly
func TestPGStore_NeverFetched_LastFetchedZero(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()
	f, err := s.AddFeed(ctx, AddFeedInput{Scope: scope, ScopeID: id, FeedURL: "https://e.com/zero.xml", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.RemoveFeed(ctx, scope, id, f.ID) })
	if !f.LastFetchedAt.IsZero() {
		t.Errorf("expected zero, got %v", f.LastFetchedAt)
	}
	got, _ := s.GetFeed(ctx, f.ID)
	if !got.LastFetchedAt.IsZero() {
		t.Errorf("expected zero on get, got %v", got.LastFetchedAt)
	}
	_ = time.Now // silence unused import
}
