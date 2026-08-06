package repl

import (
	"strings"
	"testing"
)

func TestHandleBreakCache_armsThenClears(t *testing.T) {
	m := model{}
	m1, note := m.handleBreakCache([]string{"/break-cache"})
	if m1.cacheBreaker == "" {
		t.Error("first call should arm the breaker")
	}
	if !strings.Contains(note, "armed") {
		t.Errorf("note: %s", note)
	}
	m2, note2 := m1.handleBreakCache([]string{"/break-cache"})
	if m2.cacheBreaker != "" {
		t.Error("second call should clear")
	}
	if !strings.Contains(note2, "cleared") {
		t.Errorf("clear note: %s", note2)
	}
}

func TestApplyCacheBreaker_noNonce(t *testing.T) {
	got := applyCacheBreaker("you are biu", "")
	if got != "you are biu" {
		t.Errorf("empty nonce should pass through: %q", got)
	}
}

func TestApplyCacheBreaker_appendsComment(t *testing.T) {
	got := applyCacheBreaker("sys", "deadbeef")
	if !strings.HasPrefix(got, "sys") {
		t.Error("should prefix with original")
	}
	if !strings.Contains(got, "deadbeef") {
		t.Error("nonce missing in output")
	}
	if !strings.Contains(got, "<!--") {
		t.Error("should be in comment form")
	}
}

func TestNewCacheNonce_unique(t *testing.T) {
	a := newCacheNonce()
	b := newCacheNonce()
	if a == b {
		t.Error("two nonces should differ")
	}
	if len(a) != 32 {
		t.Errorf("nonce len = %d, want 32 hex chars", len(a))
	}
}
