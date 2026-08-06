package health

import (
	"testing"
	"time"
)

func TestClassifyUpstreamStatus(t *testing.T) {
	cases := map[int]FailureKind{
		429: FailureRateLimit,
		401: FailureAuth,
		403: FailureAuth,
		402: FailureBilling,
		500: FailureTransient,
		503: FailureTransient,
		0:   FailureTransient, // 网络错误无 status
	}
	for status, want := range cases {
		if got := ClassifyUpstreamStatus(status); got != want {
			t.Errorf("ClassifyUpstreamStatus(%d) = %d, want %d", status, got, want)
		}
	}
}

func TestExpBackoff(t *testing.T) {
	// level 0 = base；逐档翻倍；封顶 backoffCap。
	if got := expBackoff(0); got != backoffBase {
		t.Errorf("expBackoff(0) = %v, want %v", got, backoffBase)
	}
	if got := expBackoff(1); got != 2*backoffBase {
		t.Errorf("expBackoff(1) = %v, want %v", got, 2*backoffBase)
	}
	// 大 level 封顶，不溢出。
	if got := expBackoff(100); got != backoffCap {
		t.Errorf("expBackoff(100) = %v, want cap %v", got, backoffCap)
	}
	if got := expBackoff(-5); got != backoffBase {
		t.Errorf("expBackoff(-5) = %v, want base %v", got, backoffBase)
	}
}

// cooldownDeadline 按 kind 算恢复时刻（不连 DB，只验时长区间）。
func TestCooldownDeadline(t *testing.T) {
	s := &Supervisor{}
	s.cfg.defaults() // FailThreshold=5

	now := time.Now()
	// 429 带 Retry-After → 用 Retry-After。
	got := s.cooldownDeadline(FailureRateLimit, 90*time.Second, 1)
	if d := got.Sub(now); d < 80*time.Second || d > 100*time.Second {
		t.Errorf("rate-limit retry-after cooldown ~90s, got %v", d)
	}
	// 429 无 Retry-After → 默认短窗。
	got = s.cooldownDeadline(FailureRateLimit, 0, 1)
	if d := got.Sub(now); d < 20*time.Second || d > 40*time.Second {
		t.Errorf("rate-limit fallback ~30s, got %v", d)
	}
	// auth → 长冷却。
	got = s.cooldownDeadline(FailureAuth, 0, 1)
	if d := got.Sub(now); d < 25*time.Minute {
		t.Errorf("auth cooldown should be long (~30m), got %v", d)
	}
	// transient：failure_count=threshold(5) → level 0 = base。
	got = s.cooldownDeadline(FailureTransient, 0, 5)
	if d := got.Sub(now); d < 25*time.Second || d > 40*time.Second {
		t.Errorf("transient at threshold ~base(30s), got %v", d)
	}
}
