package transcribe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDailyCap(t *testing.T) {
	w := &Worker{DailyCapSec: 100, usage: map[string]int{}}

	if !w.allow("u1", 0) {
		t.Fatal("fresh user should be allowed")
	}
	w.record("u1", 60)
	if !w.allow("u1", 0) {
		t.Fatal("60/100 used → still under cap")
	}
	w.record("u1", 60) // now 120, over cap
	if w.allow("u1", 0) {
		t.Fatal("120/100 used → should be blocked")
	}
	// A different user is unaffected.
	if !w.allow("u2", 0) {
		t.Fatal("other user should be independent")
	}
	// Empty user is never allowed (no one to bill).
	if w.allow("", 0) {
		t.Fatal("empty user must be rejected")
	}
}

func TestCallOnce(t *testing.T) {
	var gotAuth, gotCT string
	var gotBody transcribeReq
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"text":"你好世界","duration":42.7,"language":"zh",` +
			`"segments":[{"id":0,"start":0,"end":1.5,"text":"你好"},` +
			`{"id":1,"start":1.5,"end":3,"text":"世界"}]}`))
	}))
	defer srv.Close()

	w := &Worker{
		ModelRelayURL: srv.URL,
		Model:         "paraformer-v2",
		HTTP:          &http.Client{Timeout: 5 * time.Second},
		SignFor:       func(uid string) (string, error) { return "tok-" + uid, nil },
	}
	text, dur, segJSON, err := w.callOnce(context.Background(), Job{
		EntryID:     uuid.New(),
		OwnerUserID: "user-7",
		AudioURL:    "https://cdn.example.com/ep1.mp3",
	})
	if err != nil {
		t.Fatalf("callOnce: %v", err)
	}
	if text != "你好世界" {
		t.Fatalf("text = %q", text)
	}
	if dur != 42 {
		t.Fatalf("duration = %d, want 42 (truncated)", dur)
	}
	if !strings.Contains(string(segJSON), `"start":1.5`) {
		t.Fatalf("segments JSON missing timestamps: %s", segJSON)
	}
	if gotAuth != "Bearer tok-user-7" {
		t.Fatalf("auth = %q (per-user token not applied)", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("content-type = %q", gotCT)
	}
	if gotBody.Model != "paraformer-v2" || gotBody.AudioURL != "https://cdn.example.com/ep1.mp3" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestCallOnceNoToken(t *testing.T) {
	// No SignFor + no token → fail closed, never hit the network.
	w := &Worker{ModelRelayURL: "http://unused", HTTP: &http.Client{}}
	_, _, _, err := w.callOnce(context.Background(), Job{OwnerUserID: "u1", AudioURL: "https://x/y.mp3"})
	if err == nil {
		t.Fatal("expected error when no token can be minted")
	}
}

func TestCallOnceUpstreamError(t *testing.T) {
	// model-relay's real error shape is nested {"error":{"code","message"}};
	// the worker must surface the message into ai_error, not swallow it.
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusBadGateway)
		_, _ = rw.Write([]byte(`{"error":{"code":"resolve_failed","message":"resolver: no active channel for model: paraformer-v2"}}`))
	}))
	defer srv.Close()
	w := &Worker{
		ModelRelayURL: srv.URL,
		HTTP:          &http.Client{Timeout: 5 * time.Second},
		SignFor:       func(uid string) (string, error) { return "t", nil },
	}
	_, _, _, err := w.callOnce(context.Background(), Job{OwnerUserID: "u", AudioURL: "https://x/y.mp3"})
	if err == nil || !strings.Contains(err.Error(), "no active channel") {
		t.Fatalf("expected nested upstream message surfaced, got %v", err)
	}
}

func TestExtractErrMsg(t *testing.T) {
	cases := []struct{ name, want string; raw map[string]any }{
		{"nested", "boom", map[string]any{"error": map[string]any{"message": "boom"}}},
		{"nested code only", "resolve_failed", map[string]any{"error": map[string]any{"code": "resolve_failed"}}},
		{"flat string", "down", map[string]any{"error": "down"}},
		{"message key", "msg", map[string]any{"message": "msg"}},
		{"empty", "", map[string]any{}},
	}
	for _, c := range cases {
		if got := extractErrMsg(c.raw); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
