package publisher

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
)

// TestBusPublisher_JetStreamPath verifies that when WithJetStream is
// set, Publish routes through the durable JetStream layer and a
// matching durable consumer sees the event. Skips without a JetStream-
// enabled NATS broker so CI without infra doesn't fail.
func TestBusPublisher_JetStreamPath(t *testing.T) {
	url := natsURL()
	if !brokerOK(url) {
		t.Skipf("no NATS broker at %s; skipping", url)
	}

	b, err := bus.Connect(url, "brain-publisher-it", "test")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer b.Close()
	js, err := b.JetStream()
	if err != nil {
		t.Skipf("JetStream not available: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	suffix := uniqSuffix()
	streamName := "BIUMIND_BRAIN_PT_" + suffix
	streamSubj := bus.Subject("test", "brain", suffix) + ".>"
	if err := js.EnsureStream(ctx, bus.StreamSpec{
		Name:     streamName,
		Subjects: []string{streamSubj},
		Storage:  bus.StorageMemory,
	}); err != nil {
		t.Skipf("EnsureStream: %v", err)
	}

	// Bind an observer durable consumer so we can verify the publish
	// actually landed in the stream (not just the core pub-sub bus).
	got := make(chan map[string]any, 4)
	sub, err := js.Subscribe(ctx, bus.ConsumerSpec{
		Stream:        streamName,
		Durable:       "test-observer-" + suffix,
		FilterSubject: streamSubj,
	}, func(_ context.Context, m *bus.Message) error {
		var w map[string]any
		_ = json.Unmarshal(m.Body, &w)
		got <- w
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Drain()

	// Publish via BusPublisher with JS wired. The subject is computed
	// from env="test" + sanitized topic — for the suffix to land in
	// the stream subject we have to encode it in the topic.
	p := NewBus(b, "test", nopLogger()).WithJetStream(js)
	// Topic uses dots so sanitizeSubject preserves it; it must match
	// the stream's subject pattern (biumind.test.brain.<suffix>...).
	topic := "brain." + suffix + ".x"
	// publisher.NewBus wraps subject as: biumind.<env>.brain.<sanitized topic>.<kind>
	// With env=test, subject becomes: biumind.test.brain.brain.SUFFIX.x.block.created
	// Stream pattern biumind.test.brain.SUFFIX.> won't match.
	//
	// Easiest: put the unique suffix at the start of the topic and
	// match via a stream subject with "brain.<sanitized-topic>" prefix.
	// In production each tenant naturally separates by uuid in the
	// topic so collisions don't happen.
	_ = topic // unused; we'll publish through the real path below

	// Reset to a setup that actually matches: use suffix as the topic
	// and a stream subject pattern that captures it.
	// Stream subjects must be alphanumerics-and-dots; we already used
	// `test.brain.<suffix>.>` — publisher emits
	// `biumind.test.brain.<sanitized-topic>.<kind>`. For our stream's
	// pattern to capture it, sanitized-topic needs to start with the
	// suffix.
	// Use a dot in the topic so sanitizeSubject preserves the suffix
	// as its own segment, matching the stream's `<suffix>.>` pattern.
	rawTopic := suffix + ".wiki:p1" // → sanitized "<suffix>.wiki_p1"
	if err := p.Publish(ctx, rawTopic, "block.created",
		map[string]any{"id": "b1"},
	); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-got:
		if msg["kind"] != "block.created" {
			t.Errorf("kind = %v, want block.created", msg["kind"])
		}
		if topic := msg["topic"]; topic != rawTopic {
			t.Errorf("topic = %v, want preserved-original form", topic)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message landed in JetStream stream")
	}
}

func TestBusPublisher_NoJSFallsBackToCore(t *testing.T) {
	// With Bus set but JS nil, Publish must use the core path. We don't
	// have an easy way to inspect via interface; instead verify it
	// doesn't panic and returns nil for a NoopBus.
	p := NewBus(bus.NewNoopBus(), "test", nopLogger())
	if err := p.Publish(context.Background(), "t", "k", nil); err != nil {
		t.Errorf("noop publish should be silent: %v", err)
	}
}

func TestBusPublisher_NoBusNoJSIsNoop(t *testing.T) {
	p := &BusPublisher{Env: "test", Logger: nopLogger()}
	if err := p.Publish(context.Background(), "t", "k", nil); err != nil {
		t.Errorf("nil-bus nil-js should be silent: %v", err)
	}
}

// ─── test helpers ─────────────────────────────────────

func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

func brokerOK(url string) bool {
	host := url
	if i := len("nats://"); len(url) > i {
		host = url[i:]
	}
	c, err := net.DialTimeout("tcp", host, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

var sfxCounter int64

func uniqSuffix() string {
	now := time.Now().UnixNano() + atomic.AddInt64(&sfxCounter, 1)
	out := make([]byte, 0, 16)
	for now > 0 {
		out = append(out, "ABCDEFGHIJKLMNOP"[now&15])
		now >>= 4
	}
	return string(out)
}
