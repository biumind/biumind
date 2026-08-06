// Integration tests for cross-device sync: updated_after filtering and
// transactional outbox events (brain.events). Same DATABASE_URL-gated
// pattern as store_test.go — skips without a real Postgres.

package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

type syncEvent struct {
	Type    string
	Payload map[string]any
}

// chatEvents returns the brain.events rows for the user's chat scope
// (chat:user:<uid>, see events.go), oldest first.
func chatEvents(t *testing.T, s *Store, uid uuid.UUID) []syncEvent {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `
		SELECT event_type, payload FROM brain.events
		WHERE scope = $1 ORDER BY id
	`, "chat:user:"+uid.String())
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	defer rows.Close()
	var out []syncEvent
	for rows.Next() {
		var ev syncEvent
		var pl []byte
		if err := rows.Scan(&ev.Type, &pl); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		if err := json.Unmarshal(pl, &ev.Payload); err != nil {
			t.Fatalf("payload json: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

// wipeChatEvents removes the test's outbox rows so a running poller
// doesn't pick them up afterwards. Register BEFORE DeleteThread
// defers — deletes emit their own event.
func wipeChatEvents(t *testing.T, s *Store, uid uuid.UUID) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`DELETE FROM brain.events WHERE scope = $1`,
		"chat:user:"+uid.String()); err != nil {
		t.Fatalf("wipe events: %v", err)
	}
}

func TestListThreadsUpdatedAfter(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	old, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "old", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create old: %v", err)
	}
	fresh, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "fresh", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	defer wipeChatEvents(t, s, uid)
	defer s.DeleteThread(ctx, uid, old.ID)
	defer s.DeleteThread(ctx, uid, fresh.ID)

	// Backdate old.updated_at beyond the sync window.
	if _, err := s.pool.Exec(ctx,
		`UPDATE chat.threads SET updated_at = now() - interval '1 hour'
		 WHERE id = $1`, old.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	since := time.Now().Add(-5 * time.Minute)
	got, err := s.ListThreads(ctx, ListThreadsInput{UserID: uid, UpdatedAfter: &since})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != fresh.ID {
		t.Fatalf("updated_after: got %d threads, want only fresh", len(got))
	}

	// Archived threads stay included in incremental pulls.
	if _, err := s.UpdateThread(ctx, UpdateThreadInput{
		UserID: uid, ThreadID: fresh.ID, Archived: pBool(true),
	}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, err = s.ListThreads(ctx, ListThreadsInput{UserID: uid, UpdatedAfter: &since})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(got) != 1 || !got[0].Archived {
		t.Fatalf("updated_after should include archived, got %+v", got)
	}

	// No filter → both, unchanged legacy behavior.
	got, err = s.ListThreads(ctx, ListThreadsInput{UserID: uid})
	if err != nil || len(got) != 2 {
		t.Fatalf("unfiltered list: err=%v len=%d, want 2", err, len(got))
	}
}

func TestMessageSyncEvents(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	thread, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "sync", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer wipeChatEvents(t, s, uid)
	defer s.DeleteThread(ctx, uid, thread.ID)

	// User message with terminal status → chat.message_created.
	um, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid, Role: RoleUser, Content: "hi",
	})
	if err != nil {
		t.Fatalf("user msg: %v", err)
	}
	evs := chatEvents(t, s, uid)
	if len(evs) != 1 || evs[0].Type != EventMessageCreated {
		t.Fatalf("events after user msg: %+v", evs)
	}
	p := evs[0].Payload
	if p["thread_id"] != thread.ID.String() ||
		p["message_id"] != um.ID.String() || p["role"] != RoleUser {
		t.Errorf("user event payload: %+v", p)
	}
	if pos, _ := p["position"].(float64); int64(pos) != um.Position {
		t.Errorf("position: payload %v want %d", p["position"], um.Position)
	}
	if _, leaked := p["content"]; leaked {
		t.Errorf("event payload must not carry content: %+v", p)
	}

	// Assistant placeholder (streaming) stays silent — the terminal
	// UPDATE is the announcement (cloud send path).
	asst, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleAssistant, Status: StatusStreaming,
	})
	if err != nil {
		t.Fatalf("asst placeholder: %v", err)
	}
	if n := len(chatEvents(t, s, uid)); n != 1 {
		t.Fatalf("streaming placeholder emitted; total events=%d", n)
	}

	// Final UPDATE (status→success) → assistant event.
	if _, err := s.UpdateMessage(ctx, UpdateMessageInput{
		UserID: uid, MessageID: asst.ID,
		Content: pStr("done"), Status: pStr(StatusSuccess),
	}); err != nil {
		t.Fatalf("final update: %v", err)
	}
	evs = chatEvents(t, s, uid)
	if len(evs) != 2 {
		t.Fatalf("events after final update: %+v", evs)
	}
	p = evs[1].Payload
	if p["message_id"] != asst.ID.String() || p["role"] != RoleAssistant ||
		p["thread_id"] != thread.ID.String() {
		t.Errorf("assistant event payload: %+v", p)
	}

	// Content-only edit (no status in SET) stays silent.
	if _, err := s.UpdateMessage(ctx, UpdateMessageInput{
		UserID: uid, MessageID: asst.ID, Content: pStr("edited"),
	}); err != nil {
		t.Fatalf("content edit: %v", err)
	}
	if n := len(chatEvents(t, s, uid)); n != 2 {
		t.Fatalf("content-only edit emitted; total events=%d", n)
	}

	// client_id dedup returns the existing row → no second event.
	cid := "dedup-1"
	if _, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleUser, Content: "a", ClientID: &cid,
	}); err != nil {
		t.Fatalf("dedup first: %v", err)
	}
	if _, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleUser, Content: "b", ClientID: &cid,
	}); err != nil {
		t.Fatalf("dedup second: %v", err)
	}
	if n := len(chatEvents(t, s, uid)); n != 3 {
		t.Fatalf("dedup re-emitted; total events=%d want 3", n)
	}
}

func TestThreadSyncEvents(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	thread, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "meta", SyncEnabled: true,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer wipeChatEvents(t, s, uid)
	defer s.DeleteThread(ctx, uid, thread.ID)

	// Metadata change (rename / pin / archive) → chat.thread_updated.
	if _, err := s.UpdateThread(ctx, UpdateThreadInput{
		UserID: uid, ThreadID: thread.ID, Title: pStr("renamed"),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	evs := chatEvents(t, s, uid)
	if len(evs) != 1 || evs[0].Type != EventThreadUpdated {
		t.Fatalf("events after rename: %+v", evs)
	}
	if evs[0].Payload["thread_id"] != thread.ID.String() {
		t.Errorf("thread_updated payload: %+v", evs[0].Payload)
	}

	// Delete → dedicated event type (clients drop the thread locally
	// instead of re-fetching).
	if err := s.DeleteThread(ctx, uid, thread.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	evs = chatEvents(t, s, uid)
	if len(evs) != 2 || evs[1].Type != EventThreadDeleted ||
		evs[1].Payload["thread_id"] != thread.ID.String() {
		t.Fatalf("events after delete: %+v", evs)
	}
}

func TestSyncDisabledThreadEmitsNothing(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	// sync_enabled=false: messages still persist server-side when a
	// client does upload (enforcement of "never upload" is client-side),
	// but the privacy toggle must silence the event bus entirely.
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		UserID: uid, Title: "private", SyncEnabled: false,
	})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	defer wipeChatEvents(t, s, uid)
	defer s.DeleteThread(ctx, uid, thread.ID)

	if _, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid, Role: RoleUser, Content: "secret",
	}); err != nil {
		t.Fatalf("user msg: %v", err)
	}
	asst, err := s.CreateMessage(ctx, CreateMessageInput{
		ThreadID: thread.ID, UserID: uid,
		Role: RoleAssistant, Content: "reply", Status: StatusSuccess,
	})
	if err != nil {
		t.Fatalf("asst msg: %v", err)
	}
	if _, err := s.UpdateMessage(ctx, UpdateMessageInput{
		UserID: uid, MessageID: asst.ID, Status: pStr(StatusSuccess),
	}); err != nil {
		t.Fatalf("update msg: %v", err)
	}
	if _, err := s.UpdateThread(ctx, UpdateThreadInput{
		UserID: uid, ThreadID: thread.ID, Title: pStr("renamed"),
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := s.DeleteThread(ctx, uid, thread.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if evs := chatEvents(t, s, uid); len(evs) != 0 {
		t.Fatalf("sync_enabled=false thread emitted events: %+v", evs)
	}
}

func pBool(b bool) *bool { return &b }
