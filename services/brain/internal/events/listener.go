// Package events bridges Postgres LISTEN brain_events → Realtime publisher.
//
// Each notify payload is a small JSON {scope,id,type} wakeup — the full event
// row (payload included) is fetched from brain.events by id, because pg_notify
// has a hard 8000-byte payload limit and full event JSON (e.g. note snapshots
// with content_md) exceeds it, aborting the inserting transaction (SQLSTATE
// 22023). Notify delivery happens at commit, so the row is always visible to
// the follow-up SELECT. We forward kind = type and the inner payload to
// topic = scope (e.g. "wiki:project:<uuid>").
package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Listener struct {
	Pool      *pgxpool.Pool
	Channel   string
	Publisher publisher.Publisher
	Logger    *slog.Logger
}

type notifyPayload struct {
	Scope string `json:"scope"`
	ID    int64  `json:"id"`
	Type  string `json:"type"`
}

// Run connects to Postgres, subscribes, and forwards notifications until ctx is canceled.
// Reconnects with backoff on transient errors.
func (l *Listener) Run(ctx context.Context) {
	backoff := 500 * time.Millisecond
	for {
		if err := l.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			l.Logger.Warn("listener cycle ended", "err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		return
	}
}

func (l *Listener) runOnce(ctx context.Context) error {
	conn, err := l.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+l.Channel); err != nil {
		return err
	}
	l.Logger.Info("listener connected", "channel", l.Channel)

	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var p notifyPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			l.Logger.Warn("listener: bad payload", "raw", n.Payload, "err", err)
			continue
		}
		// notify 只是叫醒铃（{scope,id,type}），完整事件回查表 —— pg_notify
		// 8000 字节上限装不下大 payload（migration 00004）。notify 在 commit
		// 时才投递，此处 SELECT 必然可见该行；events 表无 DELETE，ErrNoRows
		// 只理论存在，防御性跳过即可。
		var raw []byte
		if err := conn.QueryRow(ctx,
			`SELECT payload FROM brain.events WHERE id = $1`, p.ID,
		).Scan(&raw); err != nil {
			l.Logger.Warn("listener: event row fetch failed", "id", p.ID, "err", err)
			continue
		}
		var inner map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &inner); err != nil {
				l.Logger.Warn("listener: bad event payload json", "id", p.ID, "err", err)
				continue
			}
		}
		body := map[string]any{
			"event_id":   p.ID,
			"event_type": p.Type,
			"data":       inner,
		}
		if err := l.Publisher.Publish(ctx, p.Scope, p.Type, body); err != nil {
			l.Logger.Warn("listener: publish failed", "err", err, "scope", p.Scope, "type", p.Type)
		}
	}
}
