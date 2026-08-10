// Integration tests for deletion tombstones (P1.1, design doc
// BiuMind-Local-Data-Isolation-Design.md §4.1). Same DATABASE_URL-gated
// real-Postgres pattern as store_test.go / sync_events_test.go.

package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// wipeTombstones removes the test user's tombstone rows. Register as a
// deferred cleanup AFTER any DeleteThread/DeleteMessage defers so it
// runs last (defers are LIFO — register it FIRST).
func wipeTombstones(t *testing.T, s *Store, uid uuid.UUID) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`DELETE FROM chat.tombstones WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("wipe tombstones: %v", err)
	}
}

func listTombstones(t *testing.T, s *Store, uid uuid.UUID,
	since time.Time, limit int,
) []Tombstone {
	t.Helper()
	tombs, err := s.ListTombstones(context.Background(), uid, since, limit)
	if err != nil {
		t.Fatalf("list tombstones: %v", err)
	}
	return tombs
}

func TestDeleteThreadWritesTombstones(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	defer wipeTombstones(t, s, uid)
	defer wipeChatEvents(t, s, uid)

	thread, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "tomb", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	m1, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleUser, Content: "q", Status: StatusSuccess,
	})
	if err != nil {
		t.Fatalf("create m1: %v", err)
	}
	m2, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleAssistant, Content: "a", Status: StatusSuccess,
	})
	if err != nil {
		t.Fatalf("create m2: %v", err)
	}

	if err := s.DeleteThread(ctx, uid, thread.ID); err != nil {
		t.Fatalf("delete thread: %v", err)
	}

	tombs := listTombstones(t, s, uid, time.Time{}, 500)
	if len(tombs) != 3 {
		t.Fatalf("want 3 tombstones (thread + 2 messages), got %+v", tombs)
	}
	byKind := map[string][]uuid.UUID{}
	for _, tb := range tombs {
		byKind[tb.Kind] = append(byKind[tb.Kind], tb.ID)
		if tb.UserID != uid {
			t.Errorf("tombstone user mismatch: %+v", tb)
		}
	}
	if len(byKind["thread"]) != 1 || byKind["thread"][0] != thread.ID {
		t.Errorf("thread tombstone: %+v", byKind["thread"])
	}
	if len(byKind["message"]) != 2 {
		t.Fatalf("message tombstones: %+v", byKind["message"])
	}
	got := map[uuid.UUID]bool{byKind["message"][0]: true, byKind["message"][1]: true}
	if !got[m1.ID] || !got[m2.ID] {
		t.Errorf("message tombstone ids: got %v want %v,%v",
			byKind["message"], m1.ID, m2.ID)
	}
}

func TestDeleteMessageWritesTombstone(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	defer wipeTombstones(t, s, uid)
	defer wipeChatEvents(t, s, uid)

	thread, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "tomb-msg", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer s.DeleteThread(ctx, uid, thread.ID)

	msg, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleAssistant, Content: "gone", Status: StatusSuccess,
	})
	if err != nil {
		t.Fatalf("create msg: %v", err)
	}
	if err := s.DeleteMessage(ctx, uid, msg.ID); err != nil {
		t.Fatalf("delete msg: %v", err)
	}

	tombs := listTombstones(t, s, uid, time.Time{}, 500)
	if len(tombs) != 1 || tombs[0].Kind != "message" || tombs[0].ID != msg.ID {
		t.Fatalf("want single message tombstone, got %+v", tombs)
	}
}

func TestListTombstonesSinceAndLimit(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	other := uuid.New()
	defer wipeTombstones(t, s, uid)
	defer wipeTombstones(t, s, other)

	// Seed three tombstones at controlled, distinct instants.
	base := time.Now().Add(-time.Hour).UTC()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range ids {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO chat.tombstones (kind, id, user_id, deleted_at)
			VALUES ('thread', $1, $2, $3)
		`, id, uid, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	// Cross-user row must never surface.
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO chat.tombstones (kind, id, user_id, deleted_at)
		VALUES ('thread', $1, $2, $3)
	`, uuid.New(), other, base.Add(30*time.Second)); err != nil {
		t.Fatalf("seed other: %v", err)
	}

	// since filters strictly (deleted_at > since): first row excluded.
	page := listTombstones(t, s, uid, base, 500)
	if len(page) != 2 || page[0].ID != ids[1] || page[1].ID != ids[2] {
		t.Fatalf("since page: %+v", page)
	}

	// Ascending order + limit pages.
	page = listTombstones(t, s, uid, time.Time{}, 2)
	if len(page) != 2 || page[0].ID != ids[0] || page[1].ID != ids[1] {
		t.Fatalf("limited page: %+v", page)
	}

	// Cross-user invisible.
	if page := listTombstones(t, s, uuid.New(), time.Time{}, 500); len(page) != 0 {
		t.Fatalf("foreign user saw tombstones: %+v", page)
	}
}

func TestTombstoneRetentionPrunes30Days(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	defer wipeTombstones(t, s, uid)
	defer wipeChatEvents(t, s, uid)

	// A tombstone older than the 30-day retention window.
	staleID := uuid.New()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO chat.tombstones (kind, id, user_id, deleted_at)
		VALUES ('thread', $1, $2, now() - interval '31 days')
	`, staleID, uid); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	// Any tombstone-writing delete lazily prunes the stale row.
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "gc", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if err := s.DeleteThread(ctx, uid, thread.ID); err != nil {
		t.Fatalf("delete thread: %v", err)
	}

	tombs := listTombstones(t, s, uid, time.Time{}, 500)
	if len(tombs) != 1 || tombs[0].ID != thread.ID {
		t.Fatalf("stale row not pruned or fresh row lost: %+v", tombs)
	}
}

// ─── HTTP endpoint ────────────────────────────────────

const (
	tombTestSecret   = "tombstone-test-secret-0123456789abcdef"
	tombTestIssuer   = "biumind-test"
	tombTestAudience = "biumind-brain-test"
)

type tombHarness struct {
	server *httptest.Server
	signer *bauth.Signer
	st     *Store
}

func newTombHarness(t *testing.T) *tombHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	st := New(pool)
	srv := NewServer(st,
		bauth.NewVerifier(tombTestSecret, tombTestIssuer, tombTestAudience),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	h := &tombHarness{
		server: httptest.NewServer(mux),
		signer: bauth.NewSigner(tombTestSecret, tombTestIssuer,
			tombTestAudience, 5*time.Minute),
		st: st,
	}
	t.Cleanup(func() { h.server.Close(); pool.Close() })
	return h
}

func (h *tombHarness) mintToken(uid uuid.UUID) string {
	tok, err := h.signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		panic(err)
	}
	return tok
}

func (h *tombHarness) getTombstones(t *testing.T, uid uuid.UUID,
	query string,
) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", h.server.URL+"/v1/chat/tombstones"+query, nil)
	req.Header.Set("Authorization", "Bearer "+h.mintToken(uid))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestTombstonesEndpoint(t *testing.T) {
	h := newTombHarness(t)
	ctx := context.Background()
	uid := uuid.New()
	other := uuid.New()
	defer wipeTombstones(t, h.st, uid)
	defer wipeTombstones(t, h.st, other)
	defer wipeChatEvents(t, h.st, uid)
	defer wipeChatEvents(t, h.st, other)

	// Empty page echoes the input since.
	since := "2026-01-01T00:00:00Z"
	status, body := h.getTombstones(t, uid, "?since="+since)
	if status != http.StatusOK {
		t.Fatalf("status: %d (%v)", status, body)
	}
	if body["next_since"] != since {
		t.Errorf("empty page next_since: got %v want %v",
			body["next_since"], since)
	}
	if tombs, _ := body["tombstones"].([]any); len(tombs) != 0 {
		t.Errorf("empty page tombstones: %v", tombs)
	}

	// Delete one thread per user.
	mk := func(u uuid.UUID, title string) *Thread {
		th, err := h.st.CreateThread(ctx, CreateThreadInput{
			UserID: u, Title: title, SyncEnabled: true,
		})
		if err != nil {
			t.Fatalf("create thread: %v", err)
		}
		if err := h.st.DeleteThread(ctx, u, th.ID); err != nil {
			t.Fatalf("delete thread: %v", err)
		}
		return th
	}
	mine := mk(uid, "mine")
	_ = mk(other, "foreign")

	// Full pull: exactly my one tombstone, contract shape verified.
	status, body = h.getTombstones(t, uid, "")
	if status != http.StatusOK {
		t.Fatalf("status: %d (%v)", status, body)
	}
	tombs, _ := body["tombstones"].([]any)
	if len(tombs) != 1 {
		t.Fatalf("tombstones: %v", body)
	}
	row, _ := tombs[0].(map[string]any)
	if row["id"] != mine.ID.String() || row["kind"] != "thread" {
		t.Errorf("row: %v", row)
	}
	deletedAt, err := time.Parse(time.RFC3339Nano,
		fmt.Sprint(row["deleted_at"]))
	if err != nil {
		t.Fatalf("deleted_at parse: %v", row)
	}
	if body["next_since"] != deletedAt.UTC().Format(time.RFC3339Nano) {
		t.Errorf("next_since: got %v want %v",
			body["next_since"], deletedAt.UTC().Format(time.RFC3339Nano))
	}

	// since >= deleted_at → empty page (strict >).
	status, body = h.getTombstones(t, uid,
		"?since="+deletedAt.UTC().Format(time.RFC3339Nano))
	if status != http.StatusOK {
		t.Fatalf("status: %d (%v)", status, body)
	}
	if tombs, _ := body["tombstones"].([]any); len(tombs) != 0 {
		t.Errorf("since-filtered page should be empty: %v", tombs)
	}

	// Bad since → 400.
	if status, _ := h.getTombstones(t, uid, "?since=not-a-time"); status != http.StatusBadRequest {
		t.Errorf("bad since: want 400 got %d", status)
	}

	// Limit clamped at 500 (accepted, not an error).
	if status, _ := h.getTombstones(t, uid, "?limit=9999"); status != http.StatusOK {
		t.Errorf("limit clamp: want 200 got %d", status)
	}
}
