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

func TestTrimThreadAfter(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	thread, _ := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "trim", SyncEnabled: true,
	})
	defer s.DeleteThread(ctx, uid, thread.ID)

	// 5 条消息 user/assistant 交替,模拟「问→答→问→答→问」。
	ids := make([]uuid.UUID, 0, 5)
	for i := 0; i < 5; i++ {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		m, err := s.CreateMessage(ctx, CreateMessageInput{
			ThreadID: thread.ID, UserID: uid,
			Role: role, Content: "msg",
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		ids = append(ids, m.ID)
	}
	all, _ := s.ListMessages(ctx, ListMessagesInput{
		ThreadID: thread.ID, UserID: uid, Limit: 100,
	})
	pivot := all[2] // 第二条 user 消息 = regenerate 的锚点

	// 截断 pivot 之后 → 删 ids[3], ids[4],前 3 条保留。
	deleted, err := s.TrimThreadAfter(ctx, thread.ID, uid, pivot.Position)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("deleted %d messages, want 2", len(deleted))
	}
	remain, _ := s.ListMessages(ctx, ListMessagesInput{
		ThreadID: thread.ID, UserID: uid, Limit: 100,
	})
	if len(remain) != 3 {
		t.Fatalf("remain %d, want 3", len(remain))
	}
	for _, m := range remain {
		if m.Position > pivot.Position {
			t.Errorf("message %s position %d > pivot %d", m.ID, m.Position, pivot.Position)
		}
	}

	// tombstone 已记(离线设备经同步感知删除)。
	var tombCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM chat.tombstones
		 WHERE user_id = $1 AND kind = 'message' AND id = ANY($2)
	`, uid, ids[3:]).Scan(&tombCount); err != nil {
		t.Fatalf("tombstone query: %v", err)
	}
	if tombCount != 2 {
		t.Errorf("tombstones = %d, want 2", tombCount)
	}

	// 幂等:同 position 再截一次 = 删 0 条。
	again, err := s.TrimThreadAfter(ctx, thread.ID, uid, pivot.Position)
	if err != nil || len(again) != 0 {
		t.Errorf("re-trim: ids=%v err=%v, want empty", again, err)
	}

	// 跨用户截断别人的 thread = 删 0 条(user_id 过滤)。
	other, err := s.TrimThreadAfter(ctx, thread.ID, uuid.New(), 0)
	if err != nil || len(other) != 0 {
		t.Errorf("cross-user trim: ids=%v err=%v, want empty", other, err)
	}
}
