package openai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

func TestTranslateEmbedRequest_Basic(t *testing.T) {
	a := New()
	req := &provider.EmbedRequest{
		Model: "bge-m3",
		Input: "hello world",
	}
	creds := &provider.Credentials{APIKey: "sk-test", BaseURL: "https://api.siliconflow.cn"}
	httpReq, err := a.TranslateEmbedRequest(context.Background(), req, creds)
	if err != nil {
		t.Fatalf("TranslateEmbedRequest: %v", err)
	}
	if got := httpReq.URL.String(); got != "https://api.siliconflow.cn/v1/embeddings" {
		t.Errorf("URL: %s", got)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: %q", got)
	}
	if got := httpReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: %q", got)
	}
	var body openAIEmbedRequest
	if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Model != "bge-m3" {
		t.Errorf("model: %q", body.Model)
	}
	if s, ok := body.Input.(string); !ok || s != "hello world" {
		t.Errorf("input: %v (期望 string 'hello world')", body.Input)
	}
}

func TestTranslateEmbedRequest_BatchInput(t *testing.T) {
	// OpenAI 接受 input 是 array 形态 (一次拿多条向量).
	a := New()
	req := &provider.EmbedRequest{
		Model:          "bge-m3",
		Input:          []string{"foo", "bar", "baz"},
		EncodingFormat: "float",
		Dimensions:     1024,
	}
	httpReq, err := a.TranslateEmbedRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var body openAIEmbedRequest
	_ = json.NewDecoder(httpReq.Body).Decode(&body)

	arr, ok := body.Input.([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("input: %v (期望长度 3 的 array)", body.Input)
	}
	if body.Dimensions != 1024 {
		t.Errorf("dimensions: %d", body.Dimensions)
	}
	if body.EncodingFormat != "float" {
		t.Errorf("encoding_format: %q", body.EncodingFormat)
	}
}

func TestTranslateEmbedRequest_BaseURLOverride(t *testing.T) {
	a := New()
	req := &provider.EmbedRequest{Model: "bge-m3", Input: "x"}
	cases := []struct {
		base, want string
	}{
		{"", "https://api.openai.com/v1/embeddings"},
		{"https://api.openai.com", "https://api.openai.com/v1/embeddings"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/embeddings"},
		{"https://api.siliconflow.cn/v1/", "https://api.siliconflow.cn/v1/embeddings"},
		{"http://localhost:11434/v1", "http://localhost:11434/v1/embeddings"}, // ollama
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			httpReq, err := a.TranslateEmbedRequest(context.Background(), req,
				&provider.Credentials{APIKey: "sk", BaseURL: tc.base})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if httpReq.URL.String() != tc.want {
				t.Errorf("URL: %s, want %s", httpReq.URL.String(), tc.want)
			}
		})
	}
}

func TestTranslateEmbedRequest_Errors(t *testing.T) {
	a := New()
	cases := []struct {
		name string
		req  *provider.EmbedRequest
		cred *provider.Credentials
	}{
		{"missing key", &provider.EmbedRequest{Model: "bge-m3", Input: "x"},
			&provider.Credentials{APIKey: ""}},
		{"empty input", &provider.EmbedRequest{Model: "bge-m3", Input: nil},
			&provider.Credentials{APIKey: "sk"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.TranslateEmbedRequest(context.Background(), tc.req, tc.cred); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestParseEmbedResponse_HappyPath(t *testing.T) {
	body := []byte(`{
		"object": "list",
		"data": [
			{"object":"embedding","index":0,"embedding":[0.1, -0.2, 0.3]},
			{"object":"embedding","index":1,"embedding":[0.4, 0.5, -0.6]}
		],
		"model": "bge-m3",
		"usage": {"prompt_tokens": 12, "total_tokens": 12}
	}`)
	a := New()
	out, err := a.ParseEmbedResponse(body)
	if err != nil {
		t.Fatalf("ParseEmbedResponse: %v", err)
	}
	if out.Model != "bge-m3" {
		t.Errorf("model: %q", out.Model)
	}
	if out.Object != "list" {
		t.Errorf("object: %q", out.Object)
	}
	if len(out.Data) != 2 {
		t.Fatalf("data len: %d", len(out.Data))
	}
	if len(out.Data[0].Embedding) != 3 || out.Data[0].Embedding[0] != 0.1 {
		t.Errorf("embedding[0]: %v", out.Data[0].Embedding)
	}
	if out.Data[1].Index != 1 {
		t.Errorf("data[1].index: %d", out.Data[1].Index)
	}
	if out.Usage.PromptTokens != 12 || out.Usage.TotalTokens != 12 {
		t.Errorf("usage: %+v", out.Usage)
	}
}

func TestParseEmbedResponse_BadJSON(t *testing.T) {
	a := New()
	_, err := a.ParseEmbedResponse([]byte(`{not json`))
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got %v", err)
	}
}

// 编译期断言 — 防漂移.
var _ provider.EmbedAdaptor = (*Adaptor)(nil)
