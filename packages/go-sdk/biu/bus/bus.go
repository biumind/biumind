// Package bus is a thin façade over NATS for cross-service async work.
//
// Why bother wrapping nats.go:
//   * Every service should `bus.Connect(NATS_URL)` and get an instance
//     that boots whether or not a broker is reachable. NoopBus drops
//     publishes silently and refuses subscribes — keeps `go run …`
//     productive when nobody's running NATS.
//   * Subjects are normalized through helpers (`Subject("channels",
//     "inbound", channel)`) so the convention `biumind.<env>.…` lives
//     in exactly one file.
//   * Tests use the embedded test server from nats-server/test, which
//     spins a real broker in <50ms. Other test files that don't have a
//     broker simply use NewNoopBus() and assert no Publish was lost.
//
// Wire format: every Publish takes an arbitrary JSON-marshalable
// payload + a `Headers` map. Subscribers get the raw bytes plus
// headers; deserialization is the caller's job.

package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Bus — what every consumer of this package depends on. Thin enough
// that NoopBus stays trivial and tests can fake it without nats-server.
type Bus interface {
	Publish(ctx context.Context, subject string, payload any, headers ...Header) error
	Subscribe(subject string, handler Handler) (Subscription, error)
	// JetStream returns the durable, at-least-once API. See
	// jetstream.go for details. NoopBus returns a stub that drops
	// publishes and refuses subscribes so dev runs without a broker
	// stay productive.
	JetStream() (JetStream, error)
	Close() error
	Connected() bool
}

// Header is a single key=value entry attached to a published message.
// Multiple values for the same key are sent as separate Headers.
type Header struct {
	Key, Value string
}

// Message is what handlers receive. Body is the raw payload bytes (likely
// JSON). Headers carry trace IDs, content-type, etc.
type Message struct {
	Subject string
	Body    []byte
	Headers map[string][]string
}

// Decode helpers JSON-unmarshal m.Body into out. Convenience for the
// 95% case.
func (m *Message) Decode(out any) error {
	return json.Unmarshal(m.Body, out)
}

type Handler func(*Message)

type Subscription interface {
	Drain() error
}

// ─── Subject helpers ─────────────────────────────────────

const subjectPrefix = "biumind"

// Subject joins parts with `.` and prepends the platform prefix.
//
//	Subject("dev", "channels", "inbound", "telegram")
//	  → "biumind.dev.channels.inbound.telegram"
//
// Empty parts are skipped so callers can pass an absent env without
// special-casing.
func Subject(parts ...string) string {
	out := []string{subjectPrefix}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ".")
}

// ─── Real NATS implementation ────────────────────────────

type natsBus struct {
	nc  *nats.Conn
	env string
}

// Connect dials NATS at `url` and tags the connection with `clientName`.
// `env` is "dev" / "staging" / "prod" — used by Subject to scope topics.
//
// Empty url ⇒ NoopBus.
func Connect(url, clientName, env string) (Bus, error) {
	if url == "" {
		return NewNoopBus(), nil
	}
	nc, err := nats.Connect(url,
		nats.Name(clientName),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("bus: nats connect %q: %w", url, err)
	}
	return &natsBus{nc: nc, env: env}, nil
}

func (b *natsBus) Publish(ctx context.Context, subject string, payload any, headers ...Header) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("bus: marshal: %w", err)
	}
	msg := &nats.Msg{Subject: subject, Data: body}
	if len(headers) > 0 {
		msg.Header = nats.Header{}
		for _, h := range headers {
			msg.Header.Add(h.Key, h.Value)
		}
	}
	if err := b.nc.PublishMsg(msg); err != nil {
		return fmt.Errorf("bus: publish %s: %w", subject, err)
	}
	// Force an explicit flush so callers can't accidentally lose
	// messages on a process exit. The cost is negligible (single round
	// trip to local NATS) compared to "did my webhook actually go out?"
	// being unanswerable.
	return b.nc.FlushTimeout(2 * time.Second)
}

func (b *natsBus) Subscribe(subject string, handler Handler) (Subscription, error) {
	sub, err := b.nc.Subscribe(subject, func(m *nats.Msg) {
		hdr := map[string][]string{}
		for k, vs := range m.Header {
			hdr[k] = vs
		}
		handler(&Message{Subject: m.Subject, Body: m.Data, Headers: hdr})
	})
	if err != nil {
		return nil, fmt.Errorf("bus: subscribe %s: %w", subject, err)
	}
	return &natsSubscription{sub: sub}, nil
}

func (b *natsBus) Close() error {
	if b.nc != nil {
		_ = b.nc.Drain()
		b.nc.Close()
	}
	return nil
}

func (b *natsBus) Connected() bool {
	return b.nc != nil && b.nc.IsConnected()
}

type natsSubscription struct{ sub *nats.Subscription }

func (s *natsSubscription) Drain() error { return s.sub.Drain() }

// ─── Noop implementation ─────────────────────────────────

// NoopBus drops every publish and refuses subscribes. Used when the
// service boots without a NATS_URL — keeps dev workflows productive.
type NoopBus struct{}

func NewNoopBus() *NoopBus { return &NoopBus{} }

var ErrNoopSubscribe = errors.New("bus: cannot subscribe on a noop bus (NATS_URL not set)")

func (NoopBus) Publish(ctx context.Context, subject string, payload any, headers ...Header) error {
	return nil
}
func (NoopBus) Subscribe(subject string, handler Handler) (Subscription, error) {
	return nil, ErrNoopSubscribe
}
func (NoopBus) Close() error    { return nil }
func (NoopBus) Connected() bool { return false }
