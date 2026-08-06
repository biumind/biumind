// image.go — dashscope.Adaptor 实现 provider.AsyncImageAdaptor (v0.3 M3).
//
// 阿里云万相 (wanx-* / qwen-image / hunyuan-image-via-dashscope) 走 dashscope
// 异步任务模式:
//
//   submit:  POST {base}/api/v1/services/aigc/text2image/image-synthesis
//            Headers: Authorization: Bearer / X-DashScope-Async: enable
//            Body: {"model":"wanx2.0-t2i-turbo", "input":{"prompt":"..."},
//                   "parameters":{"size":"1024*1024","n":1}}
//            Resp: {"output":{"task_id":"..."}}
//
//   poll:    GET  {base}/api/v1/tasks/{task_id}
//            Resp: {"output":{
//                     "task_status":"PENDING|RUNNING|SUCCEEDED|FAILED",
//                     "results":[{"url":"https://..."}]   // SUCCEEDED 时
//                     "code":"...","message":"..."         // FAILED 时
//                  }}
//
// 与 workers/aigc/biumind_aigc/providers/dashscope_image.py 完全对齐 (后者
// v1 走 /v1/jobs 服务端 worker, 这里给客户端用 OpenAI 兼容 sync facade
// /v1/images/generations 走 submit + poll 循环).

package dashscope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

const (
	imageSubmitPath = "/api/v1/services/aigc/text2image/image-synthesis"
	imagePollPath   = "/api/v1/tasks/" // + taskID
)

// dashscopeImageRequest — 上游 wire shape.
type dashscopeImageRequest struct {
	Model      string                       `json:"model"`
	Input      dashscopeImageInputBody      `json:"input"`
	Parameters dashscopeImageParametersBody `json:"parameters,omitempty"`
}

type dashscopeImageInputBody struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

type dashscopeImageParametersBody struct {
	Size string `json:"size,omitempty"`
	N    int    `json:"n,omitempty"`
	Seed int    `json:"seed,omitempty"`
}

// TranslateImageRequest — 构造 submit 请求 (X-DashScope-Async: enable).
// dashscope 没有同步图像端点, sync facade 始终走 async submit + poll.
func (a *Adaptor) TranslateImageRequest(
	ctx context.Context, req *provider.ImageRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("dashscope: missing API key")
	}
	if req.Prompt == "" {
		return nil, fmt.Errorf("dashscope: empty prompt")
	}

	upstream := dashscopeImageRequest{
		Model: req.Model,
		Input: dashscopeImageInputBody{
			Prompt:         req.Prompt,
			NegativePrompt: req.NegativePrompt,
		},
		Parameters: dashscopeImageParametersBody{
			Size: resolveDashscopeSize(req),
			N:    req.N,
			Seed: req.Seed,
		},
	}
	if upstream.Parameters.N <= 0 {
		upstream.Parameters.N = 1
	}

	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, fmt.Errorf("dashscope: marshal image: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+imageSubmitPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-DashScope-Async", "enable")
	return httpReq, nil
}

// ParseImageSubmit — 解 submit 响应, 拿 output.task_id.
func (a *Adaptor) ParseImageSubmit(body []byte) (string, error) {
	var or struct {
		Output struct {
			TaskID string `json:"task_id"`
		} `json:"output"`
		Code      string `json:"code,omitempty"`
		Message   string `json:"message,omitempty"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return "", fmt.Errorf("dashscope: parse submit: %w", err)
	}
	if or.Code != "" {
		return "", fmt.Errorf("dashscope submit: %s — %s", or.Code, or.Message)
	}
	if or.Output.TaskID == "" {
		return "", fmt.Errorf("dashscope submit: missing task_id (request_id=%s)", or.RequestID)
	}
	return or.Output.TaskID, nil
}

// BuildPollRequest — GET /api/v1/tasks/{task_id}.
func (a *Adaptor) BuildPollRequest(
	ctx context.Context, taskID string, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("dashscope: missing API key")
	}
	if taskID == "" {
		return nil, fmt.Errorf("dashscope: empty task_id")
	}
	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+imagePollPath+taskID, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	return httpReq, nil
}

// ParsePollResponse — 解 poll 响应规范化为 (status, result, err).
//
// 状态映射:
//
//	PENDING / QUEUING / RUNNING / UNKNOWN   → "running"  (caller 继续 poll)
//	SUCCEEDED                               → "succeeded" + ImageResponse
//	FAILED                                  → "failed" + err
func (a *Adaptor) ParsePollResponse(body []byte) (string, *provider.ImageResponse, error) {
	var or struct {
		Output struct {
			TaskStatus string `json:"task_status"`
			Code       string `json:"code,omitempty"`
			Message    string `json:"message,omitempty"`
			Results    []struct {
				URL          string `json:"url,omitempty"`
				Code         string `json:"code,omitempty"`
				Message      string `json:"message,omitempty"`
				ActualPrompt string `json:"actual_prompt,omitempty"`
				OrigPrompt   string `json:"orig_prompt,omitempty"`
			} `json:"results"`
		} `json:"output"`
		RequestID string `json:"request_id"`
		Usage     struct {
			ImageCount int `json:"image_count"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return "", nil, fmt.Errorf("dashscope: parse poll: %w", err)
	}

	switch strings.ToUpper(or.Output.TaskStatus) {
	case "PENDING", "QUEUING", "RUNNING", "UNKNOWN", "":
		return "running", nil, nil

	case "SUCCEEDED":
		resp := &provider.ImageResponse{}
		for _, r := range or.Output.Results {
			if r.URL == "" {
				continue
			}
			data := provider.ImageData{URL: r.URL}
			// actual_prompt 是上游 LLM 可能改写过的 prompt — 透传成 OpenAI
			// dall-e-3 风格 revised_prompt.
			if r.ActualPrompt != "" {
				data.RevisedPrompt = r.ActualPrompt
			}
			resp.Data = append(resp.Data, data)
		}
		if len(resp.Data) == 0 {
			return "failed", nil, fmt.Errorf("dashscope: SUCCEEDED but empty results (request_id=%s)", or.RequestID)
		}
		return "succeeded", resp, nil

	case "FAILED":
		code := or.Output.Code
		if code == "" {
			code = "UPSTREAM_FAILED"
		}
		msg := or.Output.Message
		if msg == "" {
			msg = "dashscope task failed"
		}
		return "failed", nil, fmt.Errorf("dashscope %s: %s", code, msg)

	default:
		// 未知状态 — 当 running 给个机会, caller 超时兜底
		return "running", nil, nil
	}
}

// ParseImageResponse — 同步路径不走这里 (dashscope wanx 全 async). 兜底
// 实现让 ImageAdaptor 接口 satisfy, 实际 handler 通过 AsyncImageAdaptor
// 类型断言走 submit+poll 分支不会调到.
func (a *Adaptor) ParseImageResponse(body []byte) (*provider.ImageResponse, error) {
	return nil, fmt.Errorf("%w: dashscope image is async-only, use AsyncImageAdaptor",
		provider.ErrNotImplemented)
}

// normalizeImageSize — OpenAI 用 "1024x1024", dashscope 用 "1024*1024".
// 客户端两种写法都接受 — 转成 dashscope 风格.
func normalizeImageSize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ReplaceAll(s, "x", "*")
}

// resolveDashscopeSize — 显式 Size 优先;为空时按 aspect_ratio+resolution 查
// dashscope 尺寸表(段3.6: size 映射从 worker 收敛进 adaptor)。
func resolveDashscopeSize(req *provider.ImageRequest) string {
	if s := normalizeImageSize(req.Size); s != "" {
		return s
	}
	if req.AspectRatio == "" {
		return ""
	}
	res := req.Resolution
	if res == "" {
		res = "720p"
	}
	return dashscopeAspectSizes[[2]string{req.AspectRatio, res}]
}

// dashscope 文生图常用尺寸(移植自 workers volcengine_image.py 之外的
// dashscope_image.py _ASPECT_SIZES)。
var dashscopeAspectSizes = map[[2]string]string{
	{"1:1", "720p"}:   "1024*1024",
	{"1:1", "1080p"}:  "1024*1024",
	{"16:9", "720p"}:  "1280*720",
	{"16:9", "1080p"}: "1920*1080",
	{"9:16", "720p"}:  "720*1280",
	{"9:16", "1080p"}: "1080*1920",
	{"4:3", "720p"}:   "1024*768",
	{"4:3", "1080p"}:  "1280*960",
	{"3:4", "720p"}:   "768*1024",
	{"3:4", "1080p"}:  "960*1280",
}

// 编译期断言 — 同时满足 ImageAdaptor + AsyncImageAdaptor (后者嵌入前者).
var (
	_ provider.ImageAdaptor      = (*Adaptor)(nil)
	_ provider.AsyncImageAdaptor = (*Adaptor)(nil)
)
