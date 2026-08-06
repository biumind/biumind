// Package natsbus subscribes to BiuMind realtime subjects on NATS and
// forwards each message into the local model-relay + Ledger.
//
// Subject pattern (see docs §3.4):
//
//	biumind.<env>.<service>.<entity>.realtime
//
// Wildcard subscription: "biumind.<env>.*.*.realtime"
package natsbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/biumind/biumind/services/realtime/internal/hub"
	"github.com/biumind/biumind/services/realtime/internal/ledger"
	"github.com/nats-io/nats.go"
)

// Wire-format for messages on biumind.*.*.*.realtime
type wireMsg struct {
	Topic   string          `json:"topic"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
	TraceID string          `json:"trace_id,omitempty"`
}

type Bus struct {
	NATSURL string
	Subject string // e.g. "biumind.dev.*.*.realtime"
	Hub     *hub.Hub
	Ledger  *ledger.Ledger
	Logger  *slog.Logger

	nc  *nats.Conn
	sub *nats.Subscription
}

func (b *Bus) Connect(ctx context.Context) error {
	nc, err := nats.Connect(b.NATSURL,
		nats.Name("biumind-realtime"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(5*time.Second),
	)
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	b.nc = nc

	sub, err := nc.Subscribe(b.Subject, b.handle)
	if err != nil {
		return fmt.Errorf("nats subscribe %s: %w", b.Subject, err)
	}
	b.sub = sub

	b.Logger.Info("natsbus connected", "url", b.NATSURL, "subject", b.Subject)

	// Drain on shutdown
	go func() {
		<-ctx.Done()
		_ = b.sub.Drain()
		_ = b.nc.Drain()
		b.Logger.Info("natsbus drained")
	}()
	return nil
}

func (b *Bus) handle(m *nats.Msg) {
	var w wireMsg
	if err := json.Unmarshal(m.Data, &w); err != nil {
		b.Logger.Warn("natsbus: bad message", "subject", m.Subject, "err", err)
		return
	}
	if w.Topic == "" || w.Kind == "" {
		b.Logger.Warn("natsbus: missing topic/kind", "subject", m.Subject)
		return
	}
	e := ledger.Event{
		ID:      newID(),
		Topic:   w.Topic,
		Kind:    w.Kind,
		Payload: w.Payload,
		TraceID: w.TraceID,
		TS:      time.Now(),
	}
	b.Ledger.Append(e)
	b.Hub.Publish(e)
}

// IsConnected returns true if NATS conn is live (used by /readyz).
func (b *Bus) IsConnected() bool {
	return b.nc != nil && b.nc.IsConnected()
}

// newID is a small monotonic-ish event id (good enough for ledger ordering).
// Format: <ms hex 12><nano hex 4>.
func newID() string {
	ns := time.Now().UnixNano()
	ms := ns / 1_000_000
	nano := uint32(ns) & 0xFFFF
	return fmt.Sprintf("%012x%04x", ms, nano)
}
