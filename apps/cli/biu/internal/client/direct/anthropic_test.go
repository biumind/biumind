package direct

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

func TestAnthropicDirectStream(t *testing.T) {
	stream := `event: message_start
data: {"type":"message_start"}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":", world"}}

event: message_delta
data: {"delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-ant-test" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, stream)
	}))
	defer ts.Close()

	a := NewAnthropic("sk-ant-test", ts.URL)
	frames, err := a.ChatStream(context.Background(), client.ChatRequest{
		Model:    "claude-sonnet",
		Messages: []client.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	var stops, ends int
	for f := range frames {
		switch f.Kind {
		case client.KindDelta:
			sb.WriteString(f.Text)
		case client.KindStop:
			stops++
		case client.KindEnd:
			ends++
		case client.KindError:
			t.Fatalf("error: %v", f.Err)
		}
	}
	if sb.String() != "Hello, world" {
		t.Errorf("text = %q", sb.String())
	}
	if stops != 1 || ends < 1 {
		t.Errorf("stops=%d ends=%d", stops, ends)
	}
}

func TestAnthropicDirectMissingKey(t *testing.T) {
	a := NewAnthropic("", "")
	if _, err := a.ChatStream(context.Background(), client.ChatRequest{Model: "x"}); err == nil {
		t.Fatal("expected error on empty key")
	}
}

func TestAnthropicDirect4xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer ts.Close()
	a := NewAnthropic("sk-ant-test", ts.URL)
	_, err := a.ChatStream(context.Background(), client.ChatRequest{Model: "x", Messages: []client.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if !strings.Contains(fmt.Sprint(err), "anthropic 400") {
		t.Errorf("err = %v", err)
	}
}
