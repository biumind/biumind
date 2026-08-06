package pty

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// 收集 sink:并发安全地累积收到的字节。
type sink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *sink) chunk(_ string, data []byte) {
	s.mu.Lock()
	s.buf.Write(data)
	s.mu.Unlock()
}

func (s *sink) string() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// Reattach 后,后续 PTY 输出应只到新 sink,旧 sink 不再收到。
func TestReattachReroutesOutput(t *testing.T) {
	m := NewManager()
	old := &sink{}
	exited := make(chan struct{})
	// 先 sleep 再输出 —— 留出 Reattach 的窗口,确保 "LATE" 在换 sink 之后才产生。
	err := m.Open("t1", OpenSpec{
		Cmd:  "sh",
		Args: []string{"-c", "sleep 0.25; printf LATE"},
	}, old.chunk, func(_ string, _ int, _ error) { close(exited) })
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	fresh := &sink{}
	if !m.Reattach("t1", fresh.chunk, func(_ string, _ int, _ error) { close(exited) }) {
		t.Fatal("Reattach should find live pty")
	}

	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("process did not exit in time")
	}

	if got := fresh.string(); got != "LATE" {
		t.Errorf("new sink got %q, want LATE", got)
	}
	if got := old.string(); got != "" {
		t.Errorf("old sink should get nothing after reattach, got %q", got)
	}
}

// Reattach 未知 id 返回 false。
func TestReattachUnknownIDFalse(t *testing.T) {
	m := NewManager()
	if m.Reattach("nope", func(string, []byte) {}, func(string, int, error) {}) {
		t.Error("Reattach on unknown id should return false")
	}
}
