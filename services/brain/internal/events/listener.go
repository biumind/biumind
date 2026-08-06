// Package events bridges Postgres LISTEN brain_events → Realtime publisher.
//
// Each notify payload is a JSON {scope,id,type,payload}. We forward kind = type
// and the inner payload to topic = scope (e.g. "wiki:project:<uuid>").
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
	Scope   string         `json:"scope"`
	ID      int64          `json:"id"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
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
		body := map[string]any{
			"event_id":   p.ID,
			"event_type": p.Type,
			"data":       p.Payload,
		}
		if err := l.Publisher.Publish(ctx, p.Scope, p.Type, body); err != nil {
			l.Logger.Warn("listener: publish failed", "err", err, "scope", p.Scope, "type", p.Type)
		}
	}
}
