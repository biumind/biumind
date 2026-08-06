package openai

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

func TestTranslateRerankRequest_Basic(t *testing.T) {
	a := New()
	req := &provider.RerankRequest{
		Model:           "BAAI/bge-reranker-v2-m3",
		Query:           "什么是向量数据库?",
		Documents:       []string{"向量数据库是…", "RAG 是…", "天气真好"},
		TopN:            2,
		ReturnDocuments: true,
	}
	creds := &provider.Credentials{APIKey: "sk-test", BaseURL: "https://api.siliconflow.cn"}
	httpReq, err := a.TranslateRerankRequest(context.Background(), req, creds)
	if err != nil {
		t.Fatalf("TranslateRerankRequest: %v", err)
	}
	if got := httpReq.URL.String(); got != "https://api.siliconflow.cn/v1/rerank" {
		t.Errorf("URL: %s", got)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: %q", got)
	}

	var body openAIRerankRequest
	if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Model != "BAAI/bge-reranker-v2-m3" {
		t.Errorf("model: %q", body.Model)
	}
	if body.Query != "什么是向量数据库?" {
		t.Errorf("query: %q", body.Query)
	}
	if len(body.Documents) != 3 {
		t.Errorf("documents len: %d", len(body.Documents))
	}
	if body.TopN != 2 {
		t.Errorf("top_n: %d", body.TopN)
	}
	if !body.ReturnDocuments {
		t.Error("return_documents should be true")
	}
}

func TestTranslateRerankRequest_Errors(t *testing.T) {
	a := New()
	docs := []string{"a"}
	cases := []struct {
		name string
		req  *provider.RerankRequest
		cred *provider.Credentials
	}{
		{"missing key", &provider.RerankRequest{Query: "q", Documents: docs},
			&provider.Credentials{APIKey: ""}},
		{"empty query", &provider.RerankRequest{Query: "", Documents: docs},
			&provider.Credentials{APIKey: "sk"}},
		{"empty docs", &provider.RerankRequest{Query: "q", Documents: nil},
			&provider.Credentials{APIKey: "sk"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.TranslateRerankRequest(context.Background(), tc.req, tc.cred); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestTranslateRerankRequest_BaseURLOverride(t *testing.T) {
	a := New()
	req := &provider.RerankRequest{
		Model: "bge-reranker", Query: "x", Documents: []string{"a", "b"},
	}
	cases := []struct {
		base, want string
	}{
		{"", "https://api.openai.com/v1/rerank"},
		{"https://api.siliconflow.cn", "https://api.siliconflow.cn/v1/rerank"},
		{"https://api.siliconflow.cn/v1", "https://api.siliconflow.cn/v1/rerank"},
		{"https://new-api.example.com/v1/", "https://new-api.example.com/v1/rerank"},
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			httpReq, err := a.TranslateRerankRequest(context.Background(), req,
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

func TestParseRerankResponse_HappyPath(t *testing.T) {
	body := []byte(`{
		"id": "req_abc",
		"results": [
			{"index": 2, "relevance_score": 0.95, "document": {"text": "topic match"}},
			{"index": 0, "relevance_score": 0.62, "document": {"text": "partial"}},
			{"index": 1, "relevance_score": 0.10}
		],
		"meta": {"billed_units": {"search_units": 1}}
	}`)
	a := New()
	out, err := a.ParseRerankResponse(body)
	if err != nil {
		t.Fatalf("ParseRerankResponse: %v", err)
	}
	if out.ID != "req_abc" {
		t.Errorf("id: %q", out.ID)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results len: %d", len(out.Results))
	}
	// 按 relevance desc 排
	if out.Results[0].Index != 2 || out.Results[0].RelevanceScore != 0.95 {
		t.Errorf("results[0]: %+v", out.Results[0])
	}
	if out.Results[0].Document == nil || out.Results[0].Document.Text != "topic match" {
		t.Errorf("results[0].document: %+v", out.Results[0].Document)
	}
	// 不带 document 字段的 entry — Document 应为 nil
	if out.Results[2].Document != nil {
		t.Errorf("results[2].document should be nil when omitted, got %+v", out.Results[2].Document)
	}
	if out.Meta.BilledUnits.SearchUnits != 1 {
		t.Errorf("billed_units.search_units: %d", out.Meta.BilledUnits.SearchUnits)
	}
}

func TestParseRerankResponse_BadJSON(t *testing.T) {
	a := New()
	_, err := a.ParseRerankResponse([]byte(`{not json`))
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// 编译期断言 — 防漂移.
var _ provider.RerankAdaptor = (*Adaptor)(nil)
