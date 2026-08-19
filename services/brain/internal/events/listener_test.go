// Integration tests for the LISTEN/NOTIFY fast path. Same deal as
// poller_test.go: real Postgres via DATABASE_URL, skip otherwise.
package events

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// 00004 regression: event payloads larger than pg_notify's 8000-byte hard
// limit must not abort the inserting transaction (SQLSTATE 22023 used to
// roll back the whole note update → client 500 → permanent sync stall for
// long notes). The trigger now sends only {scope,id,type}.
func TestNotifyTrigger_LargePayloadDoesNotFail(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	big := strings.Repeat("长", 4000) // ~12KB UTF-8 — over the 8000B limit
	id := seedEvent(t, pool, "note:user:test-large", "note.updated",
		`{"note_id":"x","content_md":"`+big+`"}`)

	// Reaching here means the INSERT committed — with the old trigger it
	// would have failed with "payload string too long".
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM brain.events WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("event row missing after insert")
	}
}

// The notify itself must be the small {scope,id,type} wakeup, and the
// Listener must fetch the full payload from the table before publishing.
func TestListener_FetchesPayloadByID(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Subscribe on a dedicated conn first so we don't miss the notify.
	sub, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer sub.Release()
	if _, err := sub.Exec(ctx, "LISTEN brain_events"); err != nil {
		t.Fatalf("listen: %v", err)
	}

	big := strings.Repeat("正", 3000) // ~9KB — old trigger couldn't carry it
	id := seedEvent(t, pool, "note:user:test-listener", "note.updated",
		`{"note_id":"n1","content_md":"`+big+`"}`)

	waitNotify := func() *pgconn.Notification {
		t.Helper()
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		defer wcancel()
		for {
			n, err := sub.Conn().WaitForNotification(wctx)
			if err != nil {
				t.Fatalf("wait notify: %v", err)
			}
			var p notifyPayload
			if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
				t.Fatalf("notify not {scope,id,type}: %q", n.Payload)
			}
			if p.ID == id {
				return n
			}
			// Notifies from concurrent tests/activity — keep waiting.
		}
	}

	n := waitNotify()
	if len(n.Payload) > 1024 {
		t.Fatalf("notify payload should be a small wakeup, got %d bytes", len(n.Payload))
	}

	// Now drive the Listener: start it first (it only forwards notifies
	// received after its own LISTEN), then insert a fresh row and check
	// that what it publishes carries the full payload fetched from the table.
	pub := &capturePublisher{}
	l := &Listener{Pool: pool, Channel: "brain_events", Publisher: pub,
		Logger: nopLogger()}
	go l.Run(ctx)
	time.Sleep(300 * time.Millisecond) // let runOnce connect + LISTEN

	id2 := seedEvent(t, pool, "note:user:test-listener", "note.updated",
		`{"note_id":"n2","content_md":"`+big+`"}`)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := pub.find(id2); got != nil {
			data, _ := got["data"].(map[string]any)
			if data["content_md"] != big {
				t.Fatalf("published data mismatch: %v", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener did not publish event %d", id2)
}

type capturePublisher struct {
	mu   sync.Mutex
	seen map[int64]map[string]any
}

func (c *capturePublisher) Publish(_ context.Context, _, _ string, body map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[int64]map[string]any{}
	}
	if id, ok := body["event_id"].(int64); ok {
		c.seen[id] = body
	}
	return nil
}

func (c *capturePublisher) find(id int64) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[id]
}
