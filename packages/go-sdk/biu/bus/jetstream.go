// JetStream — durable, at-least-once layer on top of core NATS.
//
// Use Bus.JetStream() to get a handle. The same `bus.Connect(...)` covers
// both: core pub/sub via Publish/Subscribe (ephemeral, fire-and-forget,
// fast UI fanout); JetStream via the JS handle (persisted, ACK-required,
// crash-safe).
//
// # Why this layer exists
//
// Core NATS keeps messages in RAM until they're delivered to currently
// connected subscribers. A broker restart drops everything. A subscriber
// who's offline when the message lands never sees it. That's perfect for
// realtime UI fanout (`agui:run:<id>` AG-UI events) and useless for
// inbound webhooks where losing a message means losing the user's
// Telegram reply.
//
// JetStream gives:
//
//   - Persistence to disk (configurable file vs memory store).
//   - At-least-once delivery via per-message ACKs.
//   - Durable consumers that resume from the last ACKed sequence after
//     consumer-side restart — independently of the broker.
//
// # Stream + consumer lifecycle
//
// Streams must exist before publishes land in them. Use
// EnsureStream(ctx, StreamSpec) once at boot — it's idempotent
// (CreateOrUpdate) and fast. Subjects in the spec define what the
// stream captures; we recommend `biumind.<env>.<domain>.>` so a
// single stream per domain captures every event in that namespace.
//
// Consumers are durable and named. Two consumer instances with the
// same Durable share work (work-queue semantics — each message goes
// to exactly one). Two with different Durables fan out (each gets
// its own copy). The consumer name is what survives restarts.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// JetStream is the durable side of the bus. Get one with Bus.JetStream().
type JetStream interface {
	// EnsureStream creates the stream if missing or updates it if
	// present. Idempotent — safe to call on every boot. Use this
	// rather than imperative create so config drift is a single
	// commit instead of an ops chore.
	EnsureStream(ctx context.Context, spec StreamSpec) error

	// Publish writes payload to subject. Blocks until the broker has
	// committed the message to the stream's storage. Errors are real:
	// stream missing, broker down, payload too big — all worth surfacing.
	Publish(ctx context.Context, subject string, payload any, headers ...Header) error

	// Subscribe creates or attaches to a named durable consumer and
	// invokes handler for each message. handler returns:
	//   - nil  → message is ACKed
	//   - err  → message is NAKed, redelivered after AckWait
	// Blocking handlers are fine; the consumer pulls work serially
	// per Subscribe call. Run multiple Subscribes with the same
	// Durable to scale out (NATS distributes via work-queue).
	Subscribe(ctx context.Context, spec ConsumerSpec, handler JSHandler) (Subscription, error)

	// RawJetStream is an **escape hatch** returning the underlying
	// nats.go/jetstream handle. Use only when you need an API the
	// wrapper doesn't expose (OrderedConsumer with start-sequence,
	// stream metadata inspection, etc.). For routine work prefer
	// EnsureStream / Publish / Subscribe so the wrapper can swap
	// brokers later. Returns nil for NoopBus.
	RawJetStream() jetstream.JetStream
}

// StreamSpec describes a JetStream stream. Names must be valid NATS
// stream identifiers (alphanumerics, `_`, `-`).
type StreamSpec struct {
	Name      string         // e.g. "BIUMIND_CHANNELS"
	Subjects  []string       // e.g. ["biumind.dev.channels.>"]
	MaxAge    time.Duration  // 0 = forever; default 7d
	Retention RetentionPolicy
	Storage   StorageType
	Replicas  int // 0 = 1
}

type RetentionPolicy int

const (
	// RetentionLimits keeps messages until MaxAge / size cap. Default,
	// useful for replay + audit.
	RetentionLimits RetentionPolicy = iota
	// RetentionInterest deletes when no consumer is interested. Lighter
	// disk; do NOT use when consumer may be offline at message time.
	RetentionInterest
	// RetentionWorkQueue deletes after first ACK. Strict work-queue;
	// breaks fanout (only one consumer per message).
	RetentionWorkQueue
)

type StorageType int

const (
	StorageFile   StorageType = iota // default; survives broker restart
	StorageMemory                    // ephemeral, faster, useful for tests
)

// ConsumerSpec describes a pull-mode durable consumer.
type ConsumerSpec struct {
	Stream  string // must match an existing stream name
	Durable string // consumer name; same name = shared work queue

	// FilterSubject narrows which subjects from the stream this
	// consumer sees. Empty = every subject in the stream.
	FilterSubject string

	// AckWait — how long the broker waits for an ACK before redelivering.
	// 0 = 30s.
	AckWait time.Duration

	// MaxDeliver — give-up threshold. After this many redelivery
	// attempts the message goes to the stream's "max-deliver" pile
	// (lost unless someone reads it). 0 = 5.
	MaxDeliver int
}

// JSHandler — return nil to ACK, error to NAK. Panics are caught by
// the consume loop and treated as NAK + a logged stack.
type JSHandler func(ctx context.Context, m *Message) error

// jsBus implements JetStream against a real connection.
type jsBus struct {
	js     jetstream.JetStream
	env    string
	logger *slog.Logger
}

// JetStream returns the durable handle. NoopBus returns a noop JS
// implementation that drops publishes and refuses subscribes — same
// pattern as core NoopBus, so dev runs without a broker stay productive.
func (b *natsBus) JetStream() (JetStream, error) {
	if b.nc == nil {
		return nil, errors.New("bus: jetstream needs a live NATS connection")
	}
	js, err := jetstream.New(b.nc)
	if err != nil {
		return nil, fmt.Errorf("bus: jetstream init: %w", err)
	}
	return &jsBus{js: js, env: b.env, logger: slog.Default()}, nil
}

// JetStream on NoopBus — drops publishes, refuses subscribes.
func (NoopBus) JetStream() (JetStream, error) {
	return noopJS{}, nil
}

func (b *jsBus) EnsureStream(ctx context.Context, spec StreamSpec) error {
	if spec.Name == "" {
		return errors.New("bus: stream Name required")
	}
	if len(spec.Subjects) == 0 {
		return errors.New("bus: stream Subjects required")
	}
	cfg := jetstream.StreamConfig{
		Name:      spec.Name,
		Subjects:  spec.Subjects,
		Retention: toJSRetention(spec.Retention),
		Storage:   toJSStorage(spec.Storage),
		Replicas:  spec.Replicas,
		MaxAge:    spec.MaxAge,
	}
	if cfg.Replicas <= 0 {
		cfg.Replicas = 1
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 7 * 24 * time.Hour
	}
	_, err := b.js.CreateOrUpdateStream(ctx, cfg)
	if err != nil {
		return fmt.Errorf("bus: ensure stream %q: %w", spec.Name, err)
	}
	return nil
}

func (b *jsBus) Publish(ctx context.Context, subject string, payload any, headers ...Header) error {
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
	if _, err := b.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("bus: js publish %s: %w", subject, err)
	}
	return nil
}

func (b *jsBus) Subscribe(ctx context.Context, spec ConsumerSpec, handler JSHandler) (Subscription, error) {
	if spec.Stream == "" || spec.Durable == "" {
		return nil, errors.New("bus: ConsumerSpec.Stream and Durable required")
	}
	cfg := jetstream.ConsumerConfig{
		Durable:       spec.Durable,
		Name:          spec.Durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       spec.AckWait,
		MaxDeliver:    spec.MaxDeliver,
		FilterSubject: spec.FilterSubject,
	}
	if cfg.AckWait == 0 {
		cfg.AckWait = 30 * time.Second
	}
	if cfg.MaxDeliver == 0 {
		cfg.MaxDeliver = 5
	}
	cons, err := b.js.CreateOrUpdateConsumer(ctx, spec.Stream, cfg)
	if err != nil {
		return nil, fmt.Errorf("bus: ensure consumer %q on %q: %w",
			spec.Durable, spec.Stream, err)
	}
	cc, err := cons.Consume(func(m jetstream.Msg) {
		body := m.Data()
		hdr := map[string][]string{}
		for k, vs := range m.Headers() {
			hdr[k] = vs
		}
		out := &Message{Subject: m.Subject(), Body: body, Headers: hdr}

		// Defer-recover to keep the consume loop alive when handlers
		// panic. Treated as NAK so redelivery happens — likely a bug
		// the redelivery will surface again, but at least the consumer
		// keeps draining the stream.
		defer func() {
			if r := recover(); r != nil {
				b.logger.Error("jetstream handler panic; nak'd",
					"err", r, "subject", m.Subject())
				_ = m.Nak()
			}
		}()

		if err := handler(ctx, out); err != nil {
			b.logger.Warn("jetstream handler returned error; nak'd",
				"err", err, "subject", m.Subject())
			_ = m.Nak()
			return
		}
		_ = m.Ack()
	})
	if err != nil {
		return nil, fmt.Errorf("bus: consume %q: %w", spec.Durable, err)
	}
	return &jsSubscription{cc: cc}, nil
}

// jsSubscription wraps the consume context so callers get the same
// Drain/Close shape as core subscriptions.
type jsSubscription struct{ cc jetstream.ConsumeContext }

func (s *jsSubscription) Drain() error {
	if s.cc != nil {
		s.cc.Stop()
	}
	return nil
}

func (b *jsBus) RawJetStream() jetstream.JetStream { return b.js }

// noopJS — JetStream interface for NoopBus.
type noopJS struct{}

func (noopJS) EnsureStream(_ context.Context, _ StreamSpec) error { return nil }
func (noopJS) Publish(_ context.Context, _ string, _ any, _ ...Header) error {
	return nil
}
func (noopJS) RawJetStream() jetstream.JetStream { return nil }
func (noopJS) Subscribe(_ context.Context, _ ConsumerSpec, _ JSHandler) (Subscription, error) {
	return nil, ErrNoopSubscribe
}

// ─── translation helpers ────────────────────────────────

func toJSRetention(r RetentionPolicy) jetstream.RetentionPolicy {
	switch r {
	case RetentionInterest:
		return jetstream.InterestPolicy
	case RetentionWorkQueue:
		return jetstream.WorkQueuePolicy
	default:
		return jetstream.LimitsPolicy
	}
}

func toJSStorage(s StorageType) jetstream.StorageType {
	if s == StorageMemory {
		return jetstream.MemoryStorage
	}
	return jetstream.FileStorage
}
