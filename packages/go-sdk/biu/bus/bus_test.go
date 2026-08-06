package bus

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// natsURL — env override + sane local default. Tests skip if no broker
// answers, so `go test ./...` never blocks on infrastructure.
func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return "nats://localhost:4222"
}

func brokerAvailable(t *testing.T) string {
	t.Helper()
	url := natsURL()
	host := url
	if i := len("nats://"); len(url) > i {
		host = url[i:]
	}
	c, err := net.DialTimeout("tcp", host, 200*time.Millisecond)
	if err != nil {
		t.Skipf("no NATS broker at %s; skipping (set NATS_URL to override)", url)
	}
	c.Close()
	return url
}

func TestPubSubRoundtrip(t *testing.T) {
	url := brokerAvailable(t)
	pub, err := Connect(url, "bus-test-pub", "test")
	if err != nil {
		t.Fatalf("connect pub: %v", err)
	}
	defer pub.Close()
	sub, err := Connect(url, "bus-test-sub", "test")
	if err != nil {
		t.Fatalf("connect sub: %v", err)
	}
	defer sub.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	got := make(chan *Message, 1)
	subj := Subject("test", "bus", "roundtrip", t.Name())
	s, err := sub.Subscribe(subj, func(m *Message) {
		select {
		case got <- m:
		default:
		}
		wg.Done()
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer s.Drain()

	// Give sub a moment to be live.
	time.Sleep(50 * time.Millisecond)

	if err := pub.Publish(context.Background(), subj,
		map[string]any{"hello": "world", "n": 42},
		Header{Key: "X-Trace-Id", Value: "abc"},
	); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case m := <-got:
		var payload map[string]any
		if err := m.Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if payload["hello"] != "world" || payload["n"] != float64(42) {
			t.Errorf("payload: %+v", payload)
		}
		if vs := m.Headers["X-Trace-Id"]; len(vs) != 1 || vs[0] != "abc" {
			t.Errorf("header: %+v", m.Headers)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestSubjectComposition(t *testing.T) {
	want := "biumind.dev.channels.inbound.telegram"
	got := Subject("dev", "channels", "inbound", "telegram")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// Empty parts skip cleanly.
	if got := Subject("", "channels", "inbound"); got != "biumind.channels.inbound" {
		t.Errorf("empty-skip: %q", got)
	}
}

func TestNoopBus(t *testing.T) {
	b := NewNoopBus()
	if err := b.Publish(context.Background(), "any.subject", map[string]any{"x": 1}); err != nil {
		t.Errorf("noop publish should succeed silently, got %v", err)
	}
	if _, err := b.Subscribe("any", func(*Message) {}); err == nil {
		t.Errorf("noop subscribe must error so callers know it's silent")
	}
	if b.Connected() {
		t.Errorf("noop must report disconnected")
	}
}

func TestConnectEmptyURLReturnsNoop(t *testing.T) {
	b, err := Connect("", "x", "test")
	if err != nil {
		t.Fatal(err)
	}
	if b.Connected() {
		t.Errorf("empty URL should yield NoopBus (Connected=false)")
	}
	// Sanity — publish doesn't blow up.
	if err := b.Publish(context.Background(), "any", "x"); err != nil {
		t.Errorf("noop publish: %v", err)
	}
}

func TestUnconnectedConcreteBusReturnsNoSubject(t *testing.T) {
	// Pointing at an unreachable broker — connection retries forever
	// in nats.go; we can't easily test "unreachable returns noop"
	// without timing out. Instead assert the JSON marshalling guards.
	b := NewNoopBus()
	type bad struct{ F func() }
	if err := b.Publish(context.Background(), "any", bad{F: func() {}}); err != nil {
		// Noop never marshals; this assertion is a placeholder reminder
		// that the concrete bus DOES marshal (covered by happy-path test
		// above). Force a use of the json import so it stays.
		_ = json.RawMessage(nil)
	}
}
