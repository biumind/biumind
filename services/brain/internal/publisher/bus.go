// BusPublisher — emits the same events as Realtime publisher does, but
// onto a NATS subject for cross-service consumption (Graph
// auto-extraction, future analytics workers, …).
//
// Subject convention: `biumind.<env>.brain.<topic>.<kind>`
//
// Example:
//   topic = "wiki:project:abc"
//   kind  = "block.created"
//   subject → "biumind.dev.brain.wiki:project:abc.block.created"
//
// We pass topic verbatim into the subject; NATS allows arbitrary `.`
// segments, but the existing topic colon-syntax stays intact so the
// downstream workers can use the same parsing as Realtime.

package publisher

import (
	"context"
	"log/slog"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

type BusPublisher struct {
	Bus    bus.Bus
	Env    string
	Logger *slog.Logger

	// JS — when non-nil, publishes go through JetStream (durable,
	// at-least-once). Brain emits one event per Wiki write; losing
	// these means the Graph extractor (and any future analytics
	// worker) silently drifts from the source of truth. Falls back
	// to core Bus.Publish when JS is unset (dev / no-JS broker).
	JS bus.JetStream
}

func NewBus(b bus.Bus, env string, logger *slog.Logger) *BusPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	if env == "" {
		env = "dev"
	}
	return &BusPublisher{Bus: b, Env: env, Logger: logger}
}

// WithJetStream upgrades the publisher to JetStream-backed durable
// publishing. Caller is responsible for having ensured a stream that
// captures `biumind.<env>.brain.>`.
func (p *BusPublisher) WithJetStream(js bus.JetStream) *BusPublisher {
	p.JS = js
	return p
}

func (p *BusPublisher) Publish(ctx context.Context, topic, kind string, payload map[string]any) error {
	if p.Bus == nil && p.JS == nil {
		return nil
	}
	// Topics carry colons (`wiki:project:<uuid>`) which NATS rejects.
	// Subjects also don't allow `*` or `>` outside reserved roles.
	// We sanitize aggressively — the original topic string is preserved
	// verbatim inside the body so subscribers don't lose information.
	subject := bus.Subject(p.Env, "brain", sanitizeSubject(topic), kind)
	body := map[string]any{
		"topic":   topic,
		"kind":    kind,
		"payload": payload,
	}
	if p.JS != nil {
		return p.JS.Publish(ctx, subject, body)
	}
	return p.Bus.Publish(ctx, subject, body)
}

func sanitizeSubject(s string) string {
	// NATS subjects allow alphanumerics, `-`, `_`, `.`, `*` (single-segment
	// wildcard) and `>` (terminal wildcard). Anything else — colons,
	// whitespace, control chars — must be folded so a noisy topic
	// doesn't reject the publish entirely.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Tee fans publishes out to N publishers. First-error short-circuits
// (the rest don't run); this matches the existing Realtime publisher's
// "drop on error" semantics — we'd rather know about the failure than
// silently mask it via fan-in.
type Tee struct {
	Publishers []Publisher
}

func NewTee(ps ...Publisher) *Tee { return &Tee{Publishers: ps} }

func (t *Tee) Publish(ctx context.Context, topic, kind string, payload map[string]any) error {
	for _, p := range t.Publishers {
		if p == nil {
			continue
		}
		if err := p.Publish(ctx, topic, kind, payload); err != nil {
			return err
		}
	}
	return nil
}
