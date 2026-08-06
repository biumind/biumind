// Integration tests against a real Postgres (docker compose).
//
// Skips when DATABASE_URL is unset, so `go test ./...` on a laptop
// without docker just no-ops.

package chat

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test " +
			"(run docker compose up -d postgres first)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	// Each test runs against a unique user_id so they don't collide.
	return New(pool), pool.Close
}

func TestThreadCRUD(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	model := "claude-opus-4-7"
	created, err := s.CreateThread(ctx, CreateThreadInput{
		UserID:      uid,
		Title:       "test thread",
		Model:       &model,
		SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.UserID != uid || created.Title != "test thread" {
		t.Errorf("unexpected: %+v", created)
	}

	got, err := s.GetThread(ctx, uid, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("id mismatch")
	}

	// Cross-tenant access must be ErrNotFound (not 403).
	_, err = s.GetThread(ctx, uuid.New(), created.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for other user, got %v", err)
	}

	// Update
	pinned := true
	newTitle := "renamed"
	updated, err := s.UpdateThread(ctx, UpdateThreadInput{
		UserID: uid, ThreadID: created.ID,
		Title: &newTitle, Pinned: &pinned,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "renamed" || !updated.Pinned {
		t.Errorf("update missed: %+v", updated)
	}

	// List
	threads, err := s.ListThreads(ctx, ListThreadsInput{UserID: uid})
	if err != nil || len(threads) != 1 {
		t.Errorf("list: err=%v len=%d", err, len(threads))
	}

	// Delete
	if err := s.DeleteThread(ctx, uid, created.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := s.DeleteThread(ctx, uid, created.ID); err != ErrNotFound {
		t.Errorf("second delete should ErrNotFound, got %v", err)
	}
}

func TestMessageDedup(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	thread, _ := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "dedup", SyncEnabled: true,
	})
	defer s.DeleteThread(ctx, uid, thread.ID)

	clientID := "req-XYZ"
	first, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleUser, Content: "hi", ClientID: &clientID,
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Second insert with same client_id should return original.
	second, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleUser, Content: "different content but same client_id",
		ClientID: &clientID,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("dedup failed: ids %s != %s", first.ID, second.ID)
	}
	// Content of original preserved (didn't get overwritten).
	if second.Content != "hi" {
		t.Errorf("dedup overwrote content: %q", second.Content)
	}
}

func TestMessageStreamingFlow(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	thread, _ := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "stream", SyncEnabled: true,
	})
	defer s.DeleteThread(ctx, uid, thread.ID)

	// Insert assistant placeholder (streaming).
	model := "test-model"
	asst, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role:    RoleAssistant,
		Content: "",
		Status:  StatusStreaming,
		Model:   &model,
	})
	if err != nil {
		t.Fatalf("create asst: %v", err)
	}
	if asst.Status != StatusStreaming {
		t.Errorf("status: %s", asst.Status)
	}

	// Update to success after stream ends.
	final := "Hello world."
	pt, ct := 50, 12
	done, err := s.UpdateMessage(ctx, UpdateMessageInput{
		UserID: uid, MessageID: asst.ID,
		Content:          &final,
		Status:           pStr(StatusSuccess),
		PromptTokens:     &pt,
		CompletionTokens: &ct,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if done.Content != final || done.Status != StatusSuccess {
		t.Errorf("update missed: %+v", done)
	}
	if done.PromptTokens == nil || *done.PromptTokens != 50 {
		t.Errorf("tokens: %v", done.PromptTokens)
	}
}

func TestCleanupOrphanStreaming(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	thread, _ := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "cleanup", SyncEnabled: true,
	})
	defer s.DeleteThread(ctx, uid, thread.ID)

	asst, _ := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleAssistant, Status: StatusStreaming,
	})

	// Force backdated updated_at so it's "stale".
	_, err := s.pool.Exec(ctx,
		`UPDATE chat.messages SET updated_at = now() - interval '10 minutes'
		 WHERE id = $1`, asst.ID)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	n, err := s.CleanupOrphanStreaming(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 orphan; got %d", n)
	}

	got, _ := s.GetMessage(ctx, uid, asst.ID)
	if got.Status != StatusError {
		t.Errorf("orphan should be marked error, got %s", got.Status)
	}
}

func TestListMessagesPosition(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	thread, _ := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "list", SyncEnabled: true,
	})
	defer s.DeleteThread(ctx, uid, thread.ID)

	// Insert 5 messages, alternating user/assistant.
	for i := 0; i < 5; i++ {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		_, err := s.CreateMessage(ctx, CreateMessageInput{
			ThreadID: thread.ID, UserID: uid,
			Role: role, Content: "msg",
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	all, err := s.ListMessages(ctx, ListMessagesInput{
		ThreadID: thread.ID, UserID: uid, Limit: 100,
	})
	if err != nil || len(all) != 5 {
		t.Fatalf("list: err=%v len=%d", err, len(all))
	}
	// Positions are monotonically increasing.
	for i := 1; i < len(all); i++ {
		if all[i].Position <= all[i-1].Position {
			t.Errorf("position not increasing at %d: %d ≤ %d",
				i, all[i].Position, all[i-1].Position)
		}
	}

	// Pagination with after_position.
	cut := all[2].Position
	rest, _ := s.ListMessages(ctx, ListMessagesInput{
		ThreadID: thread.ID, UserID: uid, AfterPosition: &cut, Limit: 100,
	})
	if len(rest) != 2 {
		t.Errorf("after_position pagination: got %d expected 2", len(rest))
	}
}
