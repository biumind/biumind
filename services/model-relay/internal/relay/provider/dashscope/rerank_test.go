package dashscope

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

func TestTranslateRerankRequest_DashScopeShape(t *testing.T) {
	a := New()
	req := &provider.RerankRequest{
		Model:           "gte-rerank-v2",
		Query:           "向量数据库",
		Documents:       []string{"Milvus 是向量数据库", "今天天气好"},
		TopN:            2,
		ReturnDocuments: true,
	}
	creds := &provider.Credentials{APIKey: "sk-test"}

	httpReq, err := a.TranslateRerankRequest(context.Background(), req, creds)
	if err != nil {
		t.Fatalf("TranslateRerankRequest: %v", err)
	}
	want := "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
	if got := httpReq.URL.String(); got != want {
		t.Errorf("URL: %s, want %s", got, want)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: %q", got)
	}

	var body dashscopeRerankRequest
	if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Model != "gte-rerank-v2" {
		t.Errorf("model: %q", body.Model)
	}
	// dashscope 用 input.{query, documents} 嵌套, 不像 Cohere 顶层 query
	if body.Input.Query != "向量数据库" {
		t.Errorf("input.query: %q", body.Input.Query)
	}
	if len(body.Input.Documents) != 2 {
		t.Errorf("input.documents len: %d", len(body.Input.Documents))
	}
	// 同样 top_n / return_documents 进 parameters 子对象
	if body.Parameters.TopN != 2 {
		t.Errorf("parameters.top_n: %d", body.Parameters.TopN)
	}
	if !body.Parameters.ReturnDocuments {
		t.Error("parameters.return_documents should be true")
	}
}

func TestTranslateRerankRequest_DashScopeErrors(t *testing.T) {
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

func TestParseRerankResponse_DashScopeShape(t *testing.T) {
	body := []byte(`{
		"output": {
			"results": [
				{"index": 0, "relevance_score": 0.91, "document": {"text": "Milvus 是向量数据库"}},
				{"index": 1, "relevance_score": 0.05, "document": {"text": "今天天气好"}}
			]
		},
		"usage": {"total_tokens": 24},
		"request_id": "req_zh_abc"
	}`)
	a := New()
	out, err := a.ParseRerankResponse(body)
	if err != nil {
		t.Fatalf("ParseRerankResponse: %v", err)
	}
	if out.ID != "req_zh_abc" {
		t.Errorf("id: %q (期望 request_id 透传)", out.ID)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results len: %d", len(out.Results))
	}
	if out.Results[0].RelevanceScore != 0.91 {
		t.Errorf("results[0].relevance_score: %v", out.Results[0].RelevanceScore)
	}
	if out.Results[0].Document == nil || out.Results[0].Document.Text != "Milvus 是向量数据库" {
		t.Errorf("results[0].document: %+v", out.Results[0].Document)
	}
	// dashscope total_tokens 透传到 BilledUnits.SearchUnits
	if out.Meta.BilledUnits.SearchUnits != 24 {
		t.Errorf("billed_units.search_units: %d (应该是 total_tokens 透传)",
			out.Meta.BilledUnits.SearchUnits)
	}
}

func TestParseRerankResponse_DashScopeError(t *testing.T) {
	// dashscope 业务错误 — 200 status 但 body 含 code/message.
	body := []byte(`{"code":"InvalidParameter","message":"model not found","request_id":"req_x"}`)
	a := New()
	_, err := a.ParseRerankResponse(body)
	if err == nil {
		t.Fatal("expected error from {code,message} body")
	}
	if !strings.Contains(err.Error(), "InvalidParameter") {
		t.Errorf("err should mention dashscope code: %v", err)
	}
}

// 编译期断言.
var _ provider.RerankAdaptor = (*Adaptor)(nil)
