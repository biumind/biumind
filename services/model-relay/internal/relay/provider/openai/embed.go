// embed.go — openai.Adaptor 实现 provider.EmbedAdaptor (v0.3 M2).
//
// OpenAI /v1/embeddings 是事实标准, 几乎所有上游 (OpenAI / Azure / SiliconFlow /
// 智谱 / DeepSeek / OpenRouter / TEI / Ollama / FastChat) 都走同一个 wire shape.
// bge-m3 通常托管在 SiliconFlow / 自部署 TEI / Ollama 上, 都用这套.
//
// 请求 body:
//   {
//     "model":           "bge-m3",
//     "input":           "..." | ["...", "..."],
//     "encoding_format": "float" | "base64",   // optional, OpenAI 默认 float
//     "dimensions":      1024,                  // optional, 仅 text-embedding-3-* 支持
//     "user":            "..."                  // optional, abuse 标识
//   }
//
// 响应 body:
//   {
//     "object": "list",
//     "data":   [{"object":"embedding","index":0,"embedding":[0.1, ...]}],
//     "model":  "bge-m3",
//     "usage":  {"prompt_tokens":N, "total_tokens":N}
//   }

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// openAIEmbedRequest 是上游 wire shape. input 用 any 让 OpenAI 接受
// string 或 []string 两种形态 (caller 在 EmbedRequest.Input 里给的什么
// 我们就传什么).
type openAIEmbedRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"`
	EncodingFormat string `json:"encoding_format,omitempty"`
	Dimensions     int    `json:"dimensions,omitempty"`
	User           string `json:"user,omitempty"`
}

// TranslateEmbedRequest 构造上游 POST /v1/embeddings.
func (a *Adaptor) TranslateEmbedRequest(
	ctx context.Context, req *provider.EmbedRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("openai: missing API key")
	}
	if req.Input == nil {
		return nil, fmt.Errorf("openai: empty input")
	}

	upstream := openAIEmbedRequest{
		Model:          req.Model,
		Input:          req.Input,
		EncodingFormat: req.EncodingFormat,
		Dimensions:     req.Dimensions,
		User:           req.User,
	}

	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal embed: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = creds.BaseURL
	}
	base = provider.NormalizeBaseURL(base)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v1/embeddings", bytes.NewReader(body))
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

// ParseEmbedResponse 解析非流式响应. OpenAI 把 embedding 数组按 input
// 顺序回传, index 字段冗余但我们透传.
func (a *Adaptor) ParseEmbedResponse(body []byte) (*provider.EmbedResponse, error) {
	var or struct {
		Object string `json:"object"`
		Data   []struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return nil, fmt.Errorf("openai: parse embed: %w", err)
	}
	out := &provider.EmbedResponse{
		Object: or.Object,
		Model:  or.Model,
		Usage: provider.EmbedUsage{
			PromptTokens: or.Usage.PromptTokens,
			TotalTokens:  or.Usage.TotalTokens,
		},
	}
	if out.Object == "" {
		out.Object = "list"
	}
	for _, d := range or.Data {
		obj := d.Object
		if obj == "" {
			obj = "embedding"
		}
		out.Data = append(out.Data, provider.EmbedData{
			Object:    obj,
			Index:     d.Index,
			Embedding: d.Embedding,
		})
	}
	return out, nil
}

// 编译期断言 — openai.Adaptor 现在同时满足 ChatAdaptor + EmbedAdaptor.
// (BaseAdaptor 由 Capabilities + Name 隐式满足; ChatAdaptor 由 chat.go
// 实现; EmbedAdaptor 由本文件实现.)
var _ provider.EmbedAdaptor = (*Adaptor)(nil)
