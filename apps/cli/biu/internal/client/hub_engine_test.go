package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func streamReqMinimal() engine.StreamRequest {
	return engine.StreamRequest{
		Model: "test",
		Messages: []state.Message{{
			Role: state.RoleUser,
			Content: []state.ContentBlock{{
				Type: state.ContentText, Text: "hello",
			}},
		}},
		MaxTokens: 64,
	}
}

// fakeAnthropicSSE writes a tiny scripted SSE stream so we can verify
// the auth header without depending on the real upstream.
const fakeSSE = `event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`

func TestRelayEngineUsesBearer(t *testing.T) {
	var gotAuth, gotXAPI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		gotXAPI = r.Header.Get("x-api-key")
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(fakeSSE))
	}))
	defer srv.Close()

	prov := NewRelayEngine(srv.URL, "model-relay-secret")
	ch, err := prov.Stream(context.Background(), streamReqMinimal())
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if gotAuth != "Bearer model-relay-secret" {
		t.Errorf("model-relay provider should use Bearer auth; got %q", gotAuth)
	}
	if gotXAPI != "" {
		t.Errorf("model-relay provider must NOT send x-api-key; got %q", gotXAPI)
	}
}

func TestAnthropicEngineUsesAPIKey(t *testing.T) {
	var gotAuth, gotXAPI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		gotXAPI = r.Header.Get("x-api-key")
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(fakeSSE))
	}))
	defer srv.Close()

	prov := NewAnthropicEngine("sk-real", srv.URL)
	ch, err := prov.Stream(context.Background(), streamReqMinimal())
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if !strings.HasPrefix(gotXAPI, "sk-real") {
		t.Errorf("Anthropic provider should use x-api-key; got xapi=%q auth=%q", gotXAPI, gotAuth)
	}
}
