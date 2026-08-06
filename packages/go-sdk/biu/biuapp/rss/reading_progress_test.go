package rss

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// T10.4.3 — reading_progress upsert + per-user isolation + clamp + cascade.
// DB-gated (openDB skips without DATABASE_URL).
func TestPGStore_ReadingProgress(t *testing.T) {
	pool := openDB(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	scope, id := freshScope()

	f, err := s.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: id, FeedURL: "https://e.com/progress.xml", Title: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.RemoveFeed(ctx, scope, id, f.ID) })

	if _, err := s.InsertEntries(ctx, f.ID, []ParsedEntry{
		{GUID: "p1", Title: "t1", URL: "u1", TitleHash: titleHash("t1", "u1")},
	}); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	var entryID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM rss.entries WHERE feed_id=$1 LIMIT 1`, f.ID).Scan(&entryID); err != nil {
		t.Fatalf("fetch seeded entry id: %v", err)
	}
	const userA, userB = "user-A", "user-B"

	// No row yet → found=false, pct=0.
	if pct, found, err := s.GetReadingProgress(ctx, userA, entryID); err != nil || found || pct != 0 {
		t.Fatalf("initial get = (%v, %v, %v), want (0, false, nil)", pct, found, err)
	}

	// Set then read back.
	if err := s.SetReadingProgress(ctx, userA, entryID, 0.42); err != nil {
		t.Fatal(err)
	}
	if pct, found, err := s.GetReadingProgress(ctx, userA, entryID); err != nil || !found || pct < 0.41 || pct > 0.43 {
		t.Fatalf("get after set = (%v, %v, %v), want ~0.42 found", pct, found, err)
	}

	// Upsert overwrites (not appends).
	if err := s.SetReadingProgress(ctx, userA, entryID, 0.90); err != nil {
		t.Fatal(err)
	}
	if pct, _, _ := s.GetReadingProgress(ctx, userA, entryID); pct < 0.89 || pct > 0.91 {
		t.Fatalf("upsert pct = %v, want ~0.90", pct)
	}

	// Per-user isolation: userB unaffected by userA's writes.
	if _, found, _ := s.GetReadingProgress(ctx, userB, entryID); found {
		t.Fatal("userB should have no progress for userA's writes")
	}

	// Clamp: out-of-range values are stored bounded to [0,1].
	if err := s.SetReadingProgress(ctx, userA, entryID, 5.0); err != nil {
		t.Fatal(err)
	}
	if pct, _, _ := s.GetReadingProgress(ctx, userA, entryID); pct != 1 {
		t.Fatalf("clamp high pct = %v, want 1", pct)
	}
	if err := s.SetReadingProgress(ctx, userA, entryID, -3.0); err != nil {
		t.Fatal(err)
	}
	if pct, _, _ := s.GetReadingProgress(ctx, userA, entryID); pct != 0 {
		t.Fatalf("clamp low pct = %v, want 0", pct)
	}

	// Cascade: removing the feed (→ entries) drops progress rows too.
	if err := s.RemoveFeed(ctx, scope, id, f.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM rss.reading_progress WHERE entry_id=$1`, entryID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("cascade left %d progress rows", n)
	}
}
