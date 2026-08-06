package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/files"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// stubResolver — testable Resolver: deterministic answer per file_id.
type stubResolver struct {
	urls       map[string]string // fid → presigned URL
	mediaTypes map[string]string // fid → media_type from Brain
	bytes      map[string][]byte // fid → fetched bytes
	bytesMT    map[string]string // fid → fetched media_type
	presignErr map[string]error
	fetchErr   map[string]error
	calls      []string
}

func (s *stubResolver) PresignURL(ctx context.Context, fid string) (string, string, error) {
	s.calls = append(s.calls, "presign:"+fid)
	if e, ok := s.presignErr[fid]; ok && e != nil {
		return "", "", e
	}
	if u, ok := s.urls[fid]; ok {
		return u, s.mediaTypes[fid], nil
	}
	return "", "", errors.New("no url stub for " + fid)
}

func (s *stubResolver) Fetch(ctx context.Context, fid string) ([]byte, string, error) {
	s.calls = append(s.calls, "fetch:"+fid)
	if e, ok := s.fetchErr[fid]; ok && e != nil {
		return nil, "", e
	}
	if b, ok := s.bytes[fid]; ok {
		return b, s.bytesMT[fid], nil
	}
	return nil, "", errors.New("no bytes stub for " + fid)
}

var _ files.Resolver = (*stubResolver)(nil)

func TestTranslateRequestPlainText(t *testing.T) {
	a := New()
	temp := 0.7
	req, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model:       "claude-sonnet-4-6",
			System:      provider.JSONString("You are helpful."),
			Messages:    []provider.Message{{Role: "user", Content: provider.JSONString("Hi")}},
			MaxTokens:   1024,
			Temperature: &temp,
			Stream:      true,
		},
		&provider.Credentials{APIKey: "sk-ant-test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != defaultBaseURL+"/v1/messages" {
		t.Errorf("URL = %s", req.URL)
	}
	if req.Header.Get("x-api-key") != "sk-ant-test" {
		t.Errorf("missing api key header")
	}
}

func TestTranslateRequestWithTools(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		tools, _ := got["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("expected 1 tool in upstream; got %v", got["tools"])
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	a := New()
	req, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model:    "claude-x",
			Messages: []provider.Message{{Role: "user", Content: provider.JSONString("use it")}},
			Tools: []provider.Tool{{
				Name:        "read",
				Description: "Read a file",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
					"required":   []string{"path"},
				},
			}},
		},
		&provider.Credentials{APIKey: "sk", BaseURL: ts.URL},
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestTranslateRequestWithToolResult(t *testing.T) {
	a := New()
	req, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "x",
			Messages: []provider.Message{
				{Role: "user", Content: provider.JSONString("read it")},
				{
					Role: "assistant",
					ToolCalls: []provider.ToolCall{{
						ID: "toolu_1", Name: "read",
						Input: json.RawMessage(`{"path":"x.txt"}`),
					}},
				},
				{Role: "tool", ToolCallID: "toolu_1", Content: provider.JSONString("file contents")},
			},
		},
		&provider.Credentials{APIKey: "sk"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages; got %d: %s", len(msgs), string(body))
	}
	// Third message should be user role with tool_result block.
	third := msgs[2].(map[string]any)
	if third["role"] != "user" {
		t.Errorf("expected role=user for tool_result; got %v", third["role"])
	}
	content := third["content"].([]any)
	first := content[0].(map[string]any)
	if first["type"] != "tool_result" || first["tool_use_id"] != "toolu_1" {
		t.Errorf("tool_result block wrong: %+v", first)
	}
	// Second message (assistant) should be content array with tool_use.
	second := msgs[1].(map[string]any)
	scContent := second["content"].([]any)
	scFirst := scContent[0].(map[string]any)
	if scFirst["type"] != "tool_use" || scFirst["id"] != "toolu_1" {
		t.Errorf("assistant tool_use block wrong: %+v", scFirst)
	}
}

func TestParseResponse(t *testing.T) {
	a := New()
	body := []byte(`{
		"id":"msg_01",
		"type":"message",
		"role":"assistant",
		"content":[{"type":"text","text":"Hello, world."}],
		"model":"claude-sonnet-4-6",
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":5}
	}`)
	resp, err := a.ParseResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.ContentAsString() != "Hello, world." {
		t.Errorf("content wrong: %+v", resp)
	}
}

func TestStreamAdapterTextOnly(t *testing.T) {
	a := New()
	stream := `event: message_start
data: {"type":"message_start"}

event: content_block_start
data: {"index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"index":0,"delta":{"type":"text_delta","text":", world"}}

event: content_block_stop
data: {"index":0}

event: message_delta
data: {"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`
	frames, err := a.StreamAdapter(context.Background(), strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var stops int
	for f := range frames {
		switch f.Type {
		case provider.FrameDelta:
			deltas = append(deltas, f.Delta)
		case provider.FrameStop:
			stops++
		case provider.FrameError:
			t.Fatalf("error: %v", f.Err)
		}
	}
	if len(deltas) != 2 {
		t.Errorf("deltas = %v", deltas)
	}
	if stops != 1 {
		t.Errorf("stops = %d", stops)
	}
}

func TestStreamAdapterToolUse(t *testing.T) {
	a := New()
	stream := `event: content_block_start
data: {"index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"index":0,"delta":{"type":"text_delta","text":"Let me read that."}}

event: content_block_stop
data: {"index":0}

event: content_block_start
data: {"index":1,"content_block":{"type":"tool_use","id":"toolu_xx","name":"read","input":{}}}

event: content_block_delta
data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path"}}

event: content_block_delta
data: {"index":1,"delta":{"type":"input_json_delta","partial_json":"\":\"x.txt\"}"}}

event: content_block_stop
data: {"index":1}

event: message_delta
data: {"delta":{"stop_reason":"tool_use"}}

event: message_stop
data: {}

`
	frames, err := a.StreamAdapter(context.Background(), strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	var (
		text          strings.Builder
		toolStartName string
		toolStartID   string
		argsDelta     strings.Builder
		toolEndID     string
		stops         []string
	)
	for f := range frames {
		switch f.Type {
		case provider.FrameDelta:
			text.WriteString(f.Delta)
		case provider.FrameToolCallStart:
			toolStartName = f.ToolCall.Name
			toolStartID = f.ToolCall.ID
		case provider.FrameToolCallArgs:
			argsDelta.WriteString(f.ToolCall.ArgsDelta)
		case provider.FrameToolCallEnd:
			toolEndID = f.ToolCall.ID
		case provider.FrameStop:
			stops = append(stops, f.Stop)
		case provider.FrameError:
			t.Fatalf("error: %v", f.Err)
		}
	}
	if text.String() != "Let me read that." {
		t.Errorf("text = %q", text.String())
	}
	if toolStartName != "read" || toolStartID != "toolu_xx" {
		t.Errorf("tool start = %s/%s", toolStartName, toolStartID)
	}
	if argsDelta.String() != `{"path":"x.txt"}` {
		t.Errorf("args = %q", argsDelta.String())
	}
	if toolEndID != "toolu_xx" {
		t.Errorf("tool end id = %s", toolEndID)
	}
	if len(stops) != 1 || stops[0] != "tool_use" {
		t.Errorf("stops = %v", stops)
	}
}

// ─── file resolver integration ───────────────────────────────

// helper: extract decoded request body from a TranslateRequest result.
func decodeReqBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

// helper: pull the first image block from messages[0].content.
func firstImageBlock(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages")
	}
	m0, _ := msgs[0].(map[string]any)
	contents, _ := m0["content"].([]any)
	for _, c := range contents {
		bm, _ := c.(map[string]any)
		if bm["type"] == "image" {
			return bm
		}
	}
	t.Fatalf("no image block in content: %+v", contents)
	return nil
}

func TestTranslate_FileSource_ResolvesToURL(t *testing.T) {
	res := &stubResolver{
		urls:       map[string]string{"fid-a": "https://minio.example/abc?sig=1"},
		mediaTypes: map[string]string{"fid-a": "image/png"},
	}
	a := NewWithResolver(res)
	parts := mustJSON(t, []map[string]any{
		{"type": "text", "text": "what's this"},
		{"type": "image", "source": map[string]any{
			"type": "file", "file_id": "fid-a", "media_type": "image/png",
		}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "claude-sonnet-4-6", MaxTokens: 100,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "sk-ant"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeReqBody(t, httpReq)
	img := firstImageBlock(t, body)
	src, _ := img["source"].(map[string]any)
	if src["type"] != "url" {
		t.Errorf("expected source.type=url, got %v", src)
	}
	if src["url"] != "https://minio.example/abc?sig=1" {
		t.Errorf("url = %v", src["url"])
	}
	if !contains(res.calls, "presign:fid-a") {
		t.Errorf("expected presign call, got %v", res.calls)
	}
}

func TestTranslate_FileSource_FallsBackToBase64WhenPresignFails(t *testing.T) {
	res := &stubResolver{
		presignErr: map[string]error{"fid-b": errors.New("brain down")},
		bytes:      map[string][]byte{"fid-b": []byte("raw-bytes")},
		bytesMT:    map[string]string{"fid-b": "image/jpeg"},
	}
	a := NewWithResolver(res)
	parts := mustJSON(t, []map[string]any{
		{"type": "image", "source": map[string]any{
			"type": "file", "file_id": "fid-b",
		}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "claude", MaxTokens: 10,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "k"},
	)
	if err != nil {
		t.Fatal(err)
	}
	img := firstImageBlock(t, decodeReqBody(t, httpReq))
	src, _ := img["source"].(map[string]any)
	if src["type"] != "base64" {
		t.Errorf("expected base64 fallback, got %v", src)
	}
	if src["media_type"] != "image/jpeg" {
		t.Errorf("media_type fallback: %v", src["media_type"])
	}
	if src["data"] == "" {
		t.Errorf("expected non-empty base64 data")
	}
}

func TestTranslate_FileSource_DroppedWhenBothResolveAndFetchFail(t *testing.T) {
	res := &stubResolver{
		presignErr: map[string]error{"fid-c": errors.New("p")},
		fetchErr:   map[string]error{"fid-c": errors.New("f")},
	}
	a := NewWithResolver(res)
	parts := mustJSON(t, []map[string]any{
		{"type": "text", "text": "see this"},
		{"type": "image", "source": map[string]any{"type": "file", "file_id": "fid-c"}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "claude", MaxTokens: 10,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "k"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeReqBody(t, httpReq)
	msgs, _ := body["messages"].([]any)
	m0, _ := msgs[0].(map[string]any)
	contents, _ := m0["content"].([]any)
	// Only the text block should remain — image dropped.
	if len(contents) != 1 {
		t.Fatalf("expected 1 block, got %d: %+v", len(contents), contents)
	}
	if c0 := contents[0].(map[string]any); c0["type"] != "text" {
		t.Errorf("surviving block: %+v", c0)
	}
}

func TestTranslate_FileSource_NoResolver_DropsBlock(t *testing.T) {
	a := New() // no resolver
	parts := mustJSON(t, []map[string]any{
		{"type": "text", "text": "hi"},
		{"type": "image", "source": map[string]any{"type": "file", "file_id": "x"}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "claude", MaxTokens: 10,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "k"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeReqBody(t, httpReq)
	msgs, _ := body["messages"].([]any)
	contents, _ := msgs[0].(map[string]any)["content"].([]any)
	if len(contents) != 1 || contents[0].(map[string]any)["type"] != "text" {
		t.Errorf("expected file block dropped, got %+v", contents)
	}
}

func TestTranslate_Base64SourceUntouchedByResolver(t *testing.T) {
	res := &stubResolver{} // would error if asked
	a := NewWithResolver(res)
	parts := mustJSON(t, []map[string]any{
		{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": "image/png", "data": "AAAA",
		}},
	})
	httpReq, err := a.TranslateRequest(context.Background(),
		&provider.Request{
			Model: "claude", MaxTokens: 10,
			Messages: []provider.Message{{Role: "user", Parts: parts}},
		},
		&provider.Credentials{APIKey: "k"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.calls) != 0 {
		t.Errorf("resolver should not be called for base64 source, got %v", res.calls)
	}
	src, _ := firstImageBlock(t, decodeReqBody(t, httpReq))["source"].(map[string]any)
	if src["type"] != "base64" || src["data"] != "AAAA" {
		t.Errorf("base64 mutated: %+v", src)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
