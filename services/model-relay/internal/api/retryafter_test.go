package api

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	// 整数秒。
	if got := parseRetryAfter("120"); got != 120*time.Second {
		t.Errorf("parseRetryAfter(120) = %v, want 120s", got)
	}
	// 缺失 / 非法 / 非正 → 0。
	for _, v := range []string{"", "  ", "abc", "0", "-5"} {
		if got := parseRetryAfter(v); got != 0 {
			t.Errorf("parseRetryAfter(%q) = %v, want 0", v, got)
		}
	}
	// HTTP-date（未来）→ 正时长；过去 → 0。HTTP-date 用 http.TimeFormat(GMT)，
	// 这是 RFC 7231 / http.ParseTime 认的格式（真实服务器按此发）。
	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 2*time.Minute+time.Second {
		t.Errorf("parseRetryAfter(future date) = %v, want ~2m", got)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("parseRetryAfter(past date) = %v, want 0", got)
	}
}
