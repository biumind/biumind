package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatStreamHappy(t *testing.T) {
	stream := `event: delta
data: {"text":"Hello"}

event: delta
data: {"text":", world"}

event: stop
data: {"reason":"end_turn"}

event: end
data: {}

`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, stream)
	}))
	defer ts.Close()

	c := New(ts.URL, "tok")
	frames, err := c.ChatStream(context.Background(), ChatRequest{
		Model:    "x",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var stops int
	var ends int
	for f := range frames {
		switch f.Kind {
		case KindDelta:
			text += f.Text
		case KindStop:
			stops++
		case KindEnd:
			ends++
		case KindError:
			t.Fatalf("error frame: %v", f.Err)
		}
	}
	if text != "Hello, world" {
		t.Errorf("text = %q", text)
	}
	if stops != 1 || ends != 1 {
		t.Errorf("stops=%d ends=%d", stops, ends)
	}
}

func TestChatStream4xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad model", http.StatusBadRequest)
	}))
	defer ts.Close()
	c := New(ts.URL, "tok")
	_, err := c.ChatStream(context.Background(), ChatRequest{Model: "x"})
	if err == nil {
		t.Fatal("expected error on 4xx")
	}
}

func TestPingHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer ts.Close()
	c := New(ts.URL, "")
	if err := c.PingHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
}
