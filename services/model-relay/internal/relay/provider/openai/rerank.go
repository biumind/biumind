// rerank.go — openai.Adaptor 实现 provider.RerankAdaptor (v0.3 M2.5).
//
// Rerank 没有 OpenAI 官方协议. 事实标准来自 Cohere /v1/rerank, 后被
// SiliconFlow / Jina / Voyage / Together / 新-API / TEI 等 OpenAI 兼容
// 网关原样照搬 (URL = {base}/v1/rerank, body 一模一样).
//
// 请求 body (Cohere shape):
//   {
//     "model":            "BAAI/bge-reranker-v2-m3",
//     "query":            "什么是向量数据库?",
//     "documents":        ["...", "...", "..."],
//     "top_n":            3,             // optional, 不传 = 返回全部
//     "return_documents": true            // optional, 默认 false
//   }
//
// 响应 body:
//   {
//     "id": "...",
//     "results": [
//       {"index": 2, "relevance_score": 0.95, "document": {"text": "..."}},
//       ...
//     ],
//     "meta": {"billed_units": {"search_units": 1}}
//   }
//
// "search_units" 是 Cohere 计费单位 (1 query × ≤100 docs = 1 unit), 透传
// 给客户端. M2.5 不接 model-relay 计费链路.

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// openAIRerankRequest — Cohere shape (OpenAI 兼容上游通用).
type openAIRerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
}

// TranslateRerankRequest 构造上游 POST /v1/rerank.
func (a *Adaptor) TranslateRerankRequest(
	ctx context.Context, req *provider.RerankRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("openai: missing API key")
	}
	if req.Query == "" {
		return nil, fmt.Errorf("openai: empty query")
	}
	if len(req.Documents) == 0 {
		return nil, fmt.Errorf("openai: empty documents")
	}

	upstream := openAIRerankRequest{
		Model:           req.Model,
		Query:           req.Query,
		Documents:       req.Documents,
		TopN:            req.TopN,
		ReturnDocuments: req.ReturnDocuments,
	}

	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal rerank: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = creds.BaseURL
	}
	base = provider.NormalizeBaseURL(base)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v1/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	if creds.Extra["organization"] != "" {
		httpReq.Header.Set("OpenAI-Organization", creds.Extra["organization"])
	}
	if creds.Extra["project"] != "" {
		httpReq.Header.Set("OpenAI-Project", creds.Extra["project"])
	}
	return httpReq, nil
}

// ParseRerankResponse 解析 Cohere shape 响应. results 已按 relevance_score
// desc 排好; document 字段仅 ReturnDocuments=true 时存在.
func (a *Adaptor) ParseRerankResponse(body []byte) (*provider.RerankResponse, error) {
	var or struct {
		ID      string `json:"id"`
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Document       *struct {
				Text string `json:"text"`
			} `json:"document,omitempty"`
		} `json:"results"`
		Meta struct {
			BilledUnits struct {
				SearchUnits int `json:"search_units"`
			} `json:"billed_units"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return nil, fmt.Errorf("openai: parse rerank: %w", err)
	}
	out := &provider.RerankResponse{
		ID: or.ID,
		Meta: provider.RerankMeta{
			BilledUnits: provider.RerankBilledUnits{
				SearchUnits: or.Meta.BilledUnits.SearchUnits,
			},
		},
	}
	for _, r := range or.Results {
		item := provider.RerankResult{
			Index:          r.Index,
			RelevanceScore: r.RelevanceScore,
		}
		if r.Document != nil {
			item.Document = &provider.RerankDocument{Text: r.Document.Text}
		}
		out.Results = append(out.Results, item)
	}
	return out, nil
}

// 编译期断言 — 防漂移.
var _ provider.RerankAdaptor = (*Adaptor)(nil)
