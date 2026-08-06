package api

import (
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/services/identity/internal/admin"
)

// fakeAuditor 收集 Append 调用, 用于断言.
type fakeAuditor struct {
	mu     sync.Mutex
	events []admin.AuditEvent
}

func (f *fakeAuditor) Append(ev admin.AuditEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeAuditor) Recent(n int) []admin.AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]admin.AuditEvent, 0, n)
	for i := len(f.events) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, f.events[i])
	}
	return out
}

func TestLoginThrottle_TriggersAfterThreshold(t *testing.T) {
	a := &fakeAuditor{}
	srv := &Server{Audit: a}
	throttle := &LoginThrottle{
		Window:        time.Minute,
		Threshold:     3,
		AlertCooldown: time.Minute,
		failures:      map[string][]time.Time{},
		lastAlert:     map[string]time.Time{},
	}

	// 2 次失败 — 不触发
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	if len(a.events) != 0 {
		t.Fatalf("expected 0 events at 2 failures, got %d", len(a.events))
	}
	// 第 3 次 — 触发 1 条 brute_force
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	if len(a.events) != 1 {
		t.Fatalf("expected 1 event at threshold, got %d", len(a.events))
	}
	if a.events[0].Action != "auth.login.brute_force" {
		t.Errorf("unexpected action: %s", a.events[0].Action)
	}
	if a.events[0].Success {
		t.Error("brute_force event must have Success=false")
	}
}

func TestLoginThrottle_CooldownPreventsSpam(t *testing.T) {
	a := &fakeAuditor{}
	srv := &Server{Audit: a}
	throttle := &LoginThrottle{
		Window:        time.Minute,
		Threshold:     2,
		AlertCooldown: time.Hour, // 长冷却
		failures:      map[string][]time.Time{},
		lastAlert:     map[string]time.Time{},
	}
	for i := 0; i < 10; i++ {
		throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	}
	// 阈值=2, 但冷却内只触发一次告警
	if len(a.events) != 1 {
		t.Errorf("expected 1 brute_force event under cooldown, got %d", len(a.events))
	}
}

func TestLoginThrottle_SuccessResets(t *testing.T) {
	a := &fakeAuditor{}
	srv := &Server{Audit: a}
	throttle := &LoginThrottle{
		Window:        time.Minute,
		Threshold:     3,
		AlertCooldown: time.Minute,
		failures:      map[string][]time.Time{},
		lastAlert:     map[string]time.Time{},
	}
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	throttle.recordSuccess("a@b.c", "1.2.3.4")
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	// 重置后 2 次失败 (< threshold 3) — 不触发
	if len(a.events) != 0 {
		t.Errorf("expected 0 events after success reset, got %d", len(a.events))
	}
}

func TestLoginThrottle_WindowSliding(t *testing.T) {
	a := &fakeAuditor{}
	srv := &Server{Audit: a}
	throttle := &LoginThrottle{
		Window:        100 * time.Millisecond,
		Threshold:     3,
		AlertCooldown: time.Minute,
		failures:      map[string][]time.Time{},
		lastAlert:     map[string]time.Time{},
	}
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	time.Sleep(150 * time.Millisecond) // 旧记录 prune 掉
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	throttle.recordFailure("a@b.c", "1.2.3.4", "ua", srv)
	if len(a.events) != 0 {
		t.Errorf("expected 0 events with sliding window, got %d", len(a.events))
	}
}

func TestHtmlEscape(t *testing.T) {
	got := htmlEscape(`<script>alert("x")</script>`)
	want := `&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;`
	if got != want {
		t.Errorf("htmlEscape: got %q want %q", got, want)
	}
}

func TestSendBruteForceAlert_NilSystemConfigIsNoop(t *testing.T) {
	// 不会 panic, goroutine 内的 nil-guard 必须生效.
	srv := &Server{} // SystemConfig=nil
	sendBruteForceAlert(srv, "x@y.z", "1.1.1.1", "ua", 5, time.Minute)
}

func TestLoginThrottle_EmptyEmailIsNoop(t *testing.T) {
	a := &fakeAuditor{}
	srv := &Server{Audit: a}
	throttle := NewLoginThrottle()
	throttle.Threshold = 1
	throttle.recordFailure("", "1.2.3.4", "ua", srv) // 空 email
	if len(a.events) != 0 {
		t.Error("empty email should not be tracked")
	}
}
