package bus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// jetstreamAvailable returns a JetStream handle if the broker we
// reach actually has JetStream enabled. Skips the test otherwise so
// CI without JS-enabled NATS doesn't fail.
func jetstreamAvailable(t *testing.T) (Bus, JetStream) {
	t.Helper()
	url := brokerAvailable(t)
	b, err := Connect(url, "jetstream-test", "test")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	js, err := b.JetStream()
	if err != nil {
		_ = b.Close()
		t.Skipf("JetStream not available: %v", err)
	}
	// Ping by trying to ensure a tiny test stream — if JetStream is
	// disabled server-side, this fails fast and we skip.
	streamName := "BIUMIND_TEST_PING"
	if err := js.EnsureStream(context.Background(), StreamSpec{
		Name:     streamName,
		Subjects: []string{"biumind.test.ping.>"},
		MaxAge:   1 * time.Hour,
		Storage:  StorageMemory,
	}); err != nil {
		_ = b.Close()
		t.Skipf("JetStream not enabled on broker: %v", err)
	}
	return b, js
}

// cleanupStream removes a stream after the test finishes so repeated
// runs don't pile up subjects-conflict ghosts. Errors during cleanup
// are non-fatal — the next test will skip if streams collide.
func cleanupStream(t *testing.T, js JetStream, streamName string) {
	t.Helper()
	t.Cleanup(func() {
		// Direct API call into the underlying jetstream context. Easier
		// than threading another method through the JetStream interface
		// just for tests.
		if jb, ok := js.(*jsBus); ok {
			_ = jb.js.DeleteStream(context.Background(), streamName)
		}
	})
}

func TestJetStream_PublishSubscribeRoundtrip(t *testing.T) {
	b, js := jetstreamAvailable(t)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	suffix := uniqSuffix(t)
	streamName := "BIUMIND_TEST_RT_" + suffix
	// Stream subject pattern carries the suffix so concurrent runs (or
	// the leftover state of prior failed runs) don't collide on subject
	// ownership — NATS forbids two streams to capture the same subject.
	streamSubj := Subject("test", "rt", suffix, "msg")
	subj := streamSubj + ".one"
	if err := js.EnsureStream(ctx, StreamSpec{
		Name:     streamName,
		Subjects: []string{streamSubj + ".>"},
		Storage:  StorageMemory,
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cleanupStream(t, js, streamName)

	got := make(chan *Message, 4)
	sub, err := js.Subscribe(ctx, ConsumerSpec{
		Stream:        streamName,
		Durable:       "test-rt-consumer",
		FilterSubject: streamSubj + ".>",
		AckWait:       2 * time.Second,
		MaxDeliver:    3,
	}, func(_ context.Context, m *Message) error {
		got <- m
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Drain()

	// Publish a few messages.
	for i := 0; i < 3; i++ {
		if err := js.Publish(ctx, subj, map[string]any{"i": i}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Expect all 3 to arrive within a generous deadline.
	deadline := time.After(3 * time.Second)
	received := 0
	for received < 3 {
		select {
		case <-got:
			received++
		case <-deadline:
			t.Fatalf("only got %d/3 messages", received)
		}
	}
}

func TestJetStream_DurableResumesAfterRebind(t *testing.T) {
	// Simulates consumer-side restart: publish 2, consume 2, drain, then
	// publish 1 more, re-subscribe with the same Durable name — it must
	// pick up the new message without re-delivering the first two.
	b, js := jetstreamAvailable(t)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	suffix := uniqSuffix(t)
	streamName := "BIUMIND_TEST_RESUME_" + suffix
	streamSubj := Subject("test", "resume", suffix, "msg")
	subj := streamSubj + ".one"
	if err := js.EnsureStream(ctx, StreamSpec{
		Name:     streamName,
		Subjects: []string{streamSubj + ".>"},
		Storage:  StorageMemory,
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	durable := "test-resume-consumer"

	// Round 1.
	for i := 0; i < 2; i++ {
		if err := js.Publish(ctx, subj, map[string]any{"i": i}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	got1 := make(chan int, 4)
	sub1, err := js.Subscribe(ctx, ConsumerSpec{
		Stream: streamName, Durable: durable,
		FilterSubject: streamSubj + ".>",
	}, func(_ context.Context, m *Message) error {
		var p map[string]any
		_ = m.Decode(&p)
		got1 <- int(p["i"].(float64))
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-got1:
		case <-time.After(2 * time.Second):
			t.Fatalf("round1 missed message %d", i)
		}
	}
	_ = sub1.Drain()
	// Brief gap to let ACKs flush before we tear down.
	time.Sleep(100 * time.Millisecond)

	// Round 2 — another publish, fresh subscribe with same Durable.
	if err := js.Publish(ctx, subj, map[string]any{"i": 99}); err != nil {
		t.Fatalf("publish 99: %v", err)
	}
	got2 := make(chan int, 4)
	sub2, err := js.Subscribe(ctx, ConsumerSpec{
		Stream: streamName, Durable: durable,
		FilterSubject: streamSubj + ".>",
	}, func(_ context.Context, m *Message) error {
		var p map[string]any
		_ = m.Decode(&p)
		got2 <- int(p["i"].(float64))
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	defer sub2.Drain()

	select {
	case v := <-got2:
		if v != 99 {
			t.Fatalf("got %d, want only 99 (no replay)", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("round2 missed message 99")
	}
	// Make sure no replay sneaks in within a reasonable window.
	select {
	case v := <-got2:
		t.Fatalf("unexpected replay: %d", v)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestJetStream_NakRedelivers(t *testing.T) {
	// First handler return errors → message is NAK'd → broker
	// redelivers. Second time we ACK and the test moves on.
	b, js := jetstreamAvailable(t)
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	suffix := uniqSuffix(t)
	streamName := "BIUMIND_TEST_NAK_" + suffix
	streamSubj := Subject("test", "nak", suffix, "msg")
	subj := streamSubj + ".one"
	if err := js.EnsureStream(ctx, StreamSpec{
		Name:     streamName,
		Subjects: []string{streamSubj + ".>"},
		Storage:  StorageMemory,
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cleanupStream(t, js, streamName)

	var attempts int32
	done := make(chan struct{}, 1)
	sub, err := js.Subscribe(ctx, ConsumerSpec{
		Stream: streamName, Durable: "test-nak-consumer",
		FilterSubject: streamSubj + ".>",
		AckWait:       300 * time.Millisecond, // fast redelivery for test
		MaxDeliver:    5,
	}, func(_ context.Context, _ *Message) error {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return errors.New("simulated transient failure")
		}
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Drain()

	if err := js.Publish(ctx, subj, "x"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("redelivery never succeeded; attempts=%d", atomic.LoadInt32(&attempts))
	}
	if atomic.LoadInt32(&attempts) < 2 {
		t.Fatalf("expected ≥2 attempts (NAK + redelivery); got %d", attempts)
	}
}

func TestJetStream_NoopBus(t *testing.T) {
	b := NewNoopBus()
	js, err := b.JetStream()
	if err != nil {
		t.Fatalf("noop JetStream: %v", err)
	}
	// EnsureStream is a no-op (no error).
	if err := js.EnsureStream(context.Background(), StreamSpec{Name: "X", Subjects: []string{"a.b"}}); err != nil {
		t.Errorf("noop EnsureStream returned error: %v", err)
	}
	// Publish silently drops.
	if err := js.Publish(context.Background(), "a.b", "x"); err != nil {
		t.Errorf("noop Publish: %v", err)
	}
	// Subscribe refuses, mirroring core noop.
	if _, err := js.Subscribe(context.Background(), ConsumerSpec{Stream: "X", Durable: "d"}, nil); err == nil {
		t.Error("expected ErrNoopSubscribe-style error from noop js.Subscribe")
	}
}

func TestJetStream_EnsureStreamRejectsEmptySpec(t *testing.T) {
	b, js := jetstreamAvailable(t)
	defer b.Close()
	if err := js.EnsureStream(context.Background(), StreamSpec{}); err == nil {
		t.Error("expected error on empty spec")
	}
	if err := js.EnsureStream(context.Background(), StreamSpec{Name: "X"}); err == nil {
		t.Error("expected error when Subjects empty")
	}
}

// uniqSuffix gives each test a stream name unique enough that
// concurrent test runs against the same broker don't collide.
func uniqSuffix(t *testing.T) string {
	t.Helper()
	now := time.Now().UnixNano()
	// Mangle so it stays a valid stream name (letters/digits only).
	out := make([]byte, 0, 16)
	for now > 0 {
		out = append(out, "ABCDEFGHIJKLMNOP"[now&15])
		now >>= 4
	}
	return string(out)
}

// Smoke: ensure the package compiles when used purely as a JetStream
// publisher (no subscribe loop). Should not block.
func TestJetStream_PublisherOnlyClose(t *testing.T) {
	b := NewNoopBus()
	js, err := b.JetStream()
	if err != nil {
		t.Fatalf("noop js: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = js.Publish(context.Background(), "x.y.z", "hi")
	}()
	wg.Wait()
}
