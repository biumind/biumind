package agentplane

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// fakeJS 是 bus.JetStream 的内存实现 —— 测试用。EnsureStream 记下被调一次，
// Publish 记下 (subject, payload, headers)；Subscribe 不实现（S3-3 brain 端
// 不订阅）。
type fakeJS struct {
	mu        sync.Mutex
	streams   []bus.StreamSpec
	publishes []fakePublish
}

type fakePublish struct {
	Subject string
	Payload any
	Headers []bus.Header
}

func (f *fakeJS) EnsureStream(_ context.Context, spec bus.StreamSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = append(f.streams, spec)
	return nil
}

func (f *fakeJS) Publish(_ context.Context, subject string, payload any, headers ...bus.Header) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishes = append(f.publishes, fakePublish{
		Subject: subject, Payload: payload, Headers: headers,
	})
	return nil
}

func (f *fakeJS) Subscribe(_ context.Context, _ bus.ConsumerSpec, _ bus.JSHandler) (bus.Subscription, error) {
	return nil, errors.New("fakeJS: Subscribe not implemented (S3-3 brain side does not subscribe)")
}

func (f *fakeJS) RawJetStream() jetstream.JetStream { return nil }

func TestQueue_EnsureWorkStream(t *testing.T) {
	js := &fakeJS{}
	q := NewQueue(js)
	if err := q.EnsureWorkStream(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(js.streams) != 1 {
		t.Fatalf("ensure called %d times, want 1", len(js.streams))
	}
	got := js.streams[0]
	if got.Name != WorkStreamName {
		t.Errorf("stream name=%q want %q", got.Name, WorkStreamName)
	}
	if len(got.Subjects) != 1 || got.Subjects[0] != "biu.work.>" {
		t.Errorf("subjects=%v want [biu.work.>]", got.Subjects)
	}
	if got.Retention != bus.RetentionWorkQueue {
		t.Errorf("retention=%v want WorkQueue", got.Retention)
	}
	if got.MaxAge != 24*time.Hour {
		t.Errorf("max_age=%v want 24h", got.MaxAge)
	}
}

func TestQueue_EnqueueWork(t *testing.T) {
	js := &fakeJS{}
	q := NewQueue(js)

	envID := uuid.New()
	workID := "work-abc-123"
	payload := map[string]any{"session_id": "s1", "prompt": "hello"}
	if err := q.EnqueueWork(context.Background(), envID, workID, payload); err != nil {
		t.Fatal(err)
	}

	if len(js.publishes) != 1 {
		t.Fatalf("publishes=%d want 1", len(js.publishes))
	}
	got := js.publishes[0]
	wantSubject := "biu.work." + envID.String()
	if got.Subject != wantSubject {
		t.Errorf("subject=%q want %q", got.Subject, wantSubject)
	}
	// 幂等头：Nats-Msg-Id = work_id
	var sawMsgID bool
	for _, h := range got.Headers {
		if h.Key == "Nats-Msg-Id" && h.Value == workID {
			sawMsgID = true
		}
	}
	if !sawMsgID {
		t.Errorf("missing Nats-Msg-Id=%s header in %v", workID, got.Headers)
	}
}

func TestQueue_EnqueueWork_RejectsEmptyArgs(t *testing.T) {
	q := NewQueue(&fakeJS{})
	cases := []struct {
		name   string
		envID  uuid.UUID
		workID string
	}{
		{"nil env_id", uuid.Nil, "w1"},
		{"empty work_id", uuid.New(), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := q.EnqueueWork(context.Background(), c.envID, c.workID, nil)
			if err == nil {
				t.Errorf("expected error for %s", c.name)
			}
		})
	}
}

func TestQueue_EnqueuePoolWork(t *testing.T) {
	js := &fakeJS{}
	q := NewQueue(js)

	if err := q.EnqueuePoolWork(context.Background(), "runtime-prod", "w1", "payload"); err != nil {
		t.Fatal(err)
	}
	if len(js.publishes) != 1 {
		t.Fatalf("publishes=%d", len(js.publishes))
	}
	if js.publishes[0].Subject != "biu.work.pool.runtime-prod" {
		t.Errorf("subject=%q", js.publishes[0].Subject)
	}
}

func TestQueue_NilJSReturnsError(t *testing.T) {
	q := NewQueue(nil)
	if err := q.EnsureWorkStream(context.Background()); err == nil {
		t.Error("expected error for nil JetStream")
	}
	if err := q.EnqueueWork(context.Background(), uuid.New(), "w1", nil); err == nil {
		t.Error("expected error for nil JetStream")
	}
}
