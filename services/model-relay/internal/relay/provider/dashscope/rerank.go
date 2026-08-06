// rerank.go — dashscope.Adaptor 实现 provider.RerankAdaptor (v0.3 M2.5).
//
// 阿里云百炼 rerank 不走 Cohere /v1/rerank shape, 是 dashscope 私有协议:
//
//   POST {base}/api/v1/services/rerank/text-rerank/text-rerank
//   Authorization: Bearer <api-key>
//   Body:
//     {
//       "model":      "gte-rerank-v2" | "qwen3-rerank" | ...,
//       "input": {
//         "query":     "...",
//         "documents": ["...", "..."]
//       },
//       "parameters": {
//         "top_n":            3,
//         "return_documents": true
//       }
//     }
//   Response:
//     {
//       "output":     {"results": [{"index":..., "relevance_score":..., "document":{"text":...}}]},
//       "usage":      {"total_tokens": N},
//       "request_id": "..."
//     }

package dashscope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

const rerankPath = "/api/v1/services/rerank/text-rerank/text-rerank"

// dashscopeRerankRequest — 上游 dashscope shape.
type dashscopeRerankRequest struct {
	Model      string                    `json:"model"`
	Input      dashscopeRerankInputBody  `json:"input"`
	Parameters dashscopeRerankParameters `json:"parameters,omitempty"`
}

type dashscopeRerankInputBody struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type dashscopeRerankParameters struct {
	TopN            int  `json:"top_n,omitempty"`
	ReturnDocuments bool `json:"return_documents,omitempty"`
}

// TranslateRerankRequest — 构造上游 POST .../text-rerank/text-rerank.
func (a *Adaptor) TranslateRerankRequest(
	ctx context.Context, req *provider.RerankRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("dashscope: missing API key")
	}
	if req.Query == "" {
		return nil, fmt.Errorf("dashscope: empty query")
	}
	if len(req.Documents) == 0 {
		return nil, fmt.Errorf("dashscope: empty documents")
	}

	upstream := dashscopeRerankRequest{
		Model: req.Model,
		Input: dashscopeRerankInputBody{
			Query:     req.Query,
			Documents: req.Documents,
		},
		Parameters: dashscopeRerankParameters{
			TopN:            req.TopN,
			ReturnDocuments: req.ReturnDocuments,
		},
	}

	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, fmt.Errorf("dashscope: marshal rerank: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+rerankPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

// ParseRerankResponse — 解 dashscope native 响应 → canonical RerankResponse.
// dashscope usage.total_tokens 跟 Cohere search_units 不是同一概念, 我们
// 用 1 作为 search_units 占位 (与 Cohere "1 query × ≤100 docs = 1 unit"
// 默认对齐), 真实 token 数透出 BilledUnits.SearchUnits=0 时不影响计费.
//
// 真要按 token 计费, identity pricing_book ref_type='rerank' 用 cost_basis=
// per_mtok + prompt_tokens; 留给 billing 整合阶段.
func (a *Adaptor) ParseRerankResponse(body []byte) (*provider.RerankResponse, error) {
	var or struct {
		Output struct {
			Results []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
				Document       *struct {
					Text string `json:"text"`
				} `json:"document,omitempty"`
			} `json:"results"`
		} `json:"output"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
		RequestID string `json:"request_id"`
		// 错误响应字段 (200 但 body 含 error)
		Code    string `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return nil, fmt.Errorf("dashscope: parse rerank: %w", err)
	}
	if or.Code != "" {
		return nil, fmt.Errorf("dashscope: %s — %s", or.Code, or.Message)
	}
	out := &provider.RerankResponse{
		ID: or.RequestID,
		Meta: provider.RerankMeta{
			BilledUnits: provider.RerankBilledUnits{
				// dashscope 没有 search_units 概念, total_tokens 透传到这里.
				// (M2.5 后续 billing 阶段如果加 token-based rerank 计费,
				// 这字段名要扩成 PromptTokens 之类.)
				SearchUnits: or.Usage.TotalTokens,
			},
		},
	}
	for _, r := range or.Output.Results {
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

// 编译期断言 — adaptor 现在同时满足 SpeechAdaptor (M1) + RerankAdaptor (M2.5).
var _ provider.RerankAdaptor = (*Adaptor)(nil)
