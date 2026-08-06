// Package subscriber consumes Wiki block events from NATS and runs the
// heuristic graph extractor against each block's content.
//
// Subject pattern: `biumind.<env>.brain.wiki:project:<uuid>.block.created`
// (and `.block.updated`). We subscribe with a `>` wildcard and filter
// in-process — NATS topic glob would require splitting subjects across
// known kinds, which is brittle when new event types land.
//
// Failure mode: each event is best-effort. Errors are logged at WARN
// and the next event is processed. NATS at-least-once + the extractor's
// idempotent UpsertNode/UpsertEdge means duplicates are harmless.
package subscriber

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/biumind/biumind/services/brain/internal/graph/extract"
	"github.com/biumind/biumind/services/brain/internal/graph/store"
	"github.com/google/uuid"
)

type Subscriber struct {
	Bus    bus.Bus
	Env    string
	Store  *store.Store
	Logger *slog.Logger

	// JS — when non-nil, the subscriber runs as a durable JetStream
	// consumer instead of an ephemeral core subscription. The
	// extractor's UpsertNode/UpsertEdge are idempotent, so
	// at-least-once delivery (NAK + redeliver on transient store
	// errors) is the correct semantics.
	JS bus.JetStream

	// JSStreamName — defaults to BIUMIND_BRAIN.
	JSStreamName string

	// JSDurable — defaults to brain-graph-extractor. Multiple replicas
	// with the same Durable share a work queue.
	JSDurable string

	sub bus.Subscription
}

// Run starts consuming. Returns immediately; the subscriber goroutine
// runs until ctx is cancelled or Drain() is called.
func (s *Subscriber) Run(ctx context.Context) error {
	if s.JS == nil && s.Bus == nil {
		return fmt.Errorf("graph subscriber: need either JS or Bus")
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	subject := bus.Subject(s.Env, "brain", ">")

	// JetStream path — durable, at-least-once. Preferred when wired.
	if s.JS != nil {
		streamName := s.JSStreamName
		if streamName == "" {
			streamName = "BIUMIND_BRAIN"
		}
		durable := s.JSDurable
		if durable == "" {
			durable = "brain-graph-extractor"
		}
		core := s.handle(ctx)
		sub, err := s.JS.Subscribe(ctx, bus.ConsumerSpec{
			Stream:        streamName,
			Durable:       durable,
			FilterSubject: subject,
		}, func(_ context.Context, m *bus.Message) error {
			core(m)
			return nil // ACK — handle() does its own logging
		})
		if err != nil {
			return fmt.Errorf("graph subscriber: js subscribe %w", err)
		}
		s.sub = sub
		s.Logger.Info("graph subscriber connected (jetstream)",
			"stream", streamName, "durable", durable, "subject", subject)
		go func() {
			<-ctx.Done()
			_ = sub.Drain()
		}()
		return nil
	}

	sub, err := s.Bus.Subscribe(subject, s.handle(ctx))
	if err != nil {
		return fmt.Errorf("graph subscriber: %w", err)
	}
	s.sub = sub
	s.Logger.Info("graph subscriber connected (core pubsub)", "subject", subject)
	go func() {
		<-ctx.Done()
		_ = sub.Drain()
	}()
	return nil
}

// envWire — what publisher.BusPublisher emits.
type envWire struct {
	Topic   string         `json:"topic"`
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
}

func (s *Subscriber) handle(ctx context.Context) bus.Handler {
	return func(m *bus.Message) {
		var w envWire
		if err := m.Decode(&w); err != nil {
			s.Logger.Warn("graph: bad message", "err", err, "subject", m.Subject)
			return
		}
		// Only act on block lifecycle events. Filter early so we don't
		// pay for the JSON walk on page/source/etc events.
		if w.Kind != "block.created" && w.Kind != "block.updated" {
			return
		}
		// Brain's Listener wraps NOTIFY payloads as {event_id, event_type,
		// data: <emitted payload>}. Older publishers may emit the
		// payload at the top level. Look in both places.
		body := w.Payload
		if inner, ok := w.Payload["data"].(map[string]any); ok {
			body = inner
		}

		blockID, _ := body["block_id"].(string)
		bid, err := uuid.Parse(blockID)
		if err != nil {
			s.Logger.Warn("graph: bad block_id", "err", err, "block_id", blockID, "subject", m.Subject)
			return
		}
		// project_id can be in body, or buried in topic ("wiki:project:<uuid>").
		projectID := stringFromAny(body["project_id"])
		if projectID == "" {
			projectID = parseProjectID(w.Topic)
		}
		pid, err := uuid.Parse(projectID)
		if err != nil {
			s.Logger.Warn("graph: bad project_id", "err", err, "project_id", projectID)
			return
		}

		content, _ := body["content"].(map[string]any)
		if content == nil {
			// Some publishers may stringify; try one more decode.
			if raw, ok := body["content"].(string); ok && raw != "" {
				_ = json.Unmarshal([]byte(raw), &content)
			}
		}

		cands := extract.FromBlockContent(content)
		s.Logger.DebugContext(ctx, "graph subscriber: extracted candidates",
			"subject", m.Subject, "block_id", bid, "project_id", pid,
			"kind", w.Kind, "candidates", len(cands))
		if len(cands) == 0 {
			return
		}
		s.upsert(ctx, pid, bid, cands)
	}
}

func (s *Subscriber) upsert(ctx context.Context, projectID, blockID uuid.UUID, cands []extract.Candidate) {
	for _, c := range cands {
		n, err := s.Store.UpsertNode(ctx, store.UpsertNodeInput{
			ProjectID: projectID,
			Kind:      c.Kind,
			Name:      c.Name,
			Weight:    c.Weight,
		})
		if err != nil {
			s.Logger.Warn("graph: upsert node",
				"err", err, "kind", c.Kind, "name", c.Name)
			continue
		}
		_ = s.Store.LinkBlock(ctx, blockID, n.ID, c.Weight)
	}
	s.Logger.Debug("graph: upserted",
		"block", blockID, "project", projectID, "count", len(cands))
}

// parseProjectID — extract the uuid from "wiki:project:<uuid>" topic.
func parseProjectID(topic string) string {
	if !strings.HasPrefix(topic, "wiki:project:") {
		return ""
	}
	return strings.TrimPrefix(topic, "wiki:project:")
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}
