// video.go — dashscope.Adaptor 实现 provider.AsyncVideoAdaptor (v0.3 M4).
//
// 阿里云万相视频 (wanx2.1-i2v-turbo / wanx-2.6-t2v 等) 走 dashscope 异步:
//
//   submit:  POST {base}/api/v1/services/aigc/video-generation/video-synthesis
//            Headers: Authorization Bearer / X-DashScope-Async: enable
//            Body: {
//              "model":"wanx2.1-i2v-turbo",
//              "input": {
//                "prompt":"...",
//                "img_url"|"first_frame_url"+"last_frame_url"|"reference_urls"
//              },
//              "parameters": {"size":"1280*720","duration":5,"prompt_extend":true}
//            }
//            Resp: {"output":{"task_id":"..."}}
//
//   poll:    GET {base}/api/v1/tasks/{task_id}   ← 跟 image 共用 path
//            Resp: {"output":{
//                     "task_status":"PENDING|RUNNING|SUCCEEDED|FAILED",
//                     "video_url":"https://..."         // 老形态
//                     "cover_image_url":"https://...",
//                     "results":[{"video_url":...,"cover_image_url":...}]  // 新形态
//                  }}
//
// 与 workers/aigc/biumind_aigc/providers/dashscope_video.py 完全对齐.

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

const videoSubmitPath = "/api/v1/services/aigc/video-generation/video-synthesis"

// dashscopeVideoRequest — 上游 wire shape.
type dashscopeVideoRequest struct {
	Model      string                       `json:"model"`
	Input      dashscopeVideoInputBody      `json:"input"`
	Parameters dashscopeVideoParametersBody `json:"parameters,omitempty"`
}

type dashscopeVideoInputBody struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	// FirstFrameURL 单独 → "img_url" (DashScope 旧字段); FirstFrameURL +
	// LastFrameURL → "first_frame_url" + "last_frame_url" (新字段). 这里
	// 用 omitempty 让 TranslateVideoRequest 决定填哪几个.
	ImgURL        string   `json:"img_url,omitempty"`
	FirstFrameURL string   `json:"first_frame_url,omitempty"`
	LastFrameURL  string   `json:"last_frame_url,omitempty"`
	ReferenceURLs []string `json:"reference_urls,omitempty"`
}

type dashscopeVideoParametersBody struct {
	Size         string `json:"size,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	Seed         int    `json:"seed,omitempty"`
	PromptExtend bool   `json:"prompt_extend,omitempty"`
	Watermark    bool   `json:"watermark,omitempty"`
}

// videoAspectSizes — aspect_ratio + resolution → "宽*高", 跟
// workers/aigc/biumind_aigc/providers/dashscope_video.py:_VIDEO_SIZES 同步.
var videoAspectSizes = map[[2]string]string{
	{"16:9", "720p"}:  "1280*720",
	{"16:9", "1080p"}: "1920*1080",
	{"9:16", "720p"}:  "720*1280",
	{"9:16", "1080p"}: "1080*1920",
	{"1:1", "720p"}:   "960*960",
	{"1:1", "1080p"}:  "1080*1080",
}

// TranslateVideoRequest — 构造 submit 请求 (X-DashScope-Async: enable).
func (a *Adaptor) TranslateVideoRequest(
	ctx context.Context, req *provider.VideoRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("dashscope: missing API key")
	}
	if req.Prompt == "" {
		return nil, fmt.Errorf("dashscope: empty prompt")
	}

	upstream := dashscopeVideoRequest{
		Model: req.Model,
		Input: dashscopeVideoInputBody{
			Prompt:         req.Prompt,
			NegativePrompt: req.NegativePrompt,
		},
		Parameters: dashscopeVideoParametersBody{
			Size:         resolveVideoSize(req),
			Duration:     req.DurationSeconds,
			Seed:         req.Seed,
			PromptExtend: true, // 默认开 — dashscope 智能改写 prompt 提升生成质量
		},
	}

	// 输入形态: 首尾帧 / 仅首帧 / 参考图. 首帧单独时用 img_url 字段
	// (DashScope 历史字段名), 首+尾时用 first_frame_url + last_frame_url.
	if req.FirstFrameURL != "" && req.LastFrameURL != "" {
		upstream.Input.FirstFrameURL = req.FirstFrameURL
		upstream.Input.LastFrameURL = req.LastFrameURL
	} else if req.FirstFrameURL != "" {
		upstream.Input.ImgURL = req.FirstFrameURL
	}
	if len(req.ReferenceImageURLs) > 0 {
		upstream.Input.ReferenceURLs = req.ReferenceImageURLs
	}

	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, fmt.Errorf("dashscope: marshal video: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+videoSubmitPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-DashScope-Async", "enable")
	return httpReq, nil
}

// resolveVideoSize — Size 字段优先, 否则 AspectRatio + Resolution 查表.
func resolveVideoSize(req *provider.VideoRequest) string {
	if req.Size != "" {
		return strings.ReplaceAll(req.Size, "x", "*")
	}
	if req.AspectRatio == "" {
		return ""
	}
	res := req.Resolution
	if res == "" {
		res = "720p"
	}
	if v, ok := videoAspectSizes[[2]string{req.AspectRatio, res}]; ok {
		return v
	}
	return ""
}

// ParseVideoSubmit — 同 image submit, 拿 output.task_id.
func (a *Adaptor) ParseVideoSubmit(body []byte) (string, error) {
	var or struct {
		Output struct {
			TaskID string `json:"task_id"`
		} `json:"output"`
		Code      string `json:"code,omitempty"`
		Message   string `json:"message,omitempty"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return "", fmt.Errorf("dashscope: parse video submit: %w", err)
	}
	if or.Code != "" {
		return "", fmt.Errorf("dashscope video submit: %s — %s", or.Code, or.Message)
	}
	if or.Output.TaskID == "" {
		return "", fmt.Errorf("dashscope video submit: missing task_id (request_id=%s)", or.RequestID)
	}
	return or.Output.TaskID, nil
}

// BuildVideoPollRequest — 跟 BuildPollRequest (image) 同 path
// /api/v1/tasks/{task_id}, 但走独立方法保持类型签名独立.
func (a *Adaptor) BuildVideoPollRequest(
	ctx context.Context, taskID string, creds *provider.Credentials,
) (*http.Request, error) {
	// 直接复用 image 的实现 — 同 path 同方式.
	return a.BuildPollRequest(ctx, taskID, creds)
}

// ParseVideoPollResponse — 解 dashscope poll response 兼容两种 SUCCEEDED 形态:
//  1. output.video_url + output.cover_image_url    (老 zhiying 形态)
//  2. output.results = [{video_url, cover_image_url}, ...]   (新形态)
//
// 状态映射跟 image 相同: PENDING/QUEUING/RUNNING/UNKNOWN → "running".
func (a *Adaptor) ParseVideoPollResponse(body []byte) (string, *provider.VideoResponse, error) {
	var or struct {
		Output struct {
			TaskStatus    string `json:"task_status"`
			Code          string `json:"code,omitempty"`
			Message       string `json:"message,omitempty"`
			VideoURL      string `json:"video_url,omitempty"`
			CoverImageURL string `json:"cover_image_url,omitempty"`
			Results       []struct {
				VideoURL      string `json:"video_url,omitempty"`
				CoverImageURL string `json:"cover_image_url,omitempty"`
				URL           string `json:"url,omitempty"` // 兼容个别 provider 用 url 字段
				ActualPrompt  string `json:"actual_prompt,omitempty"`
				DurationMs    int    `json:"duration_ms,omitempty"`
			} `json:"results"`
			ActualPrompt string `json:"actual_prompt,omitempty"`
		} `json:"output"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return "", nil, fmt.Errorf("dashscope: parse video poll: %w", err)
	}

	switch strings.ToUpper(or.Output.TaskStatus) {
	case "PENDING", "QUEUING", "RUNNING", "UNKNOWN", "":
		return "running", nil, nil

	case "SUCCEEDED":
		resp := &provider.VideoResponse{}
		// 形态 1: output.video_url 直接挂.
		if or.Output.VideoURL != "" {
			data := provider.VideoData{
				URL:           or.Output.VideoURL,
				CoverImageURL: or.Output.CoverImageURL,
			}
			if or.Output.ActualPrompt != "" {
				data.RevisedPrompt = or.Output.ActualPrompt
			}
			resp.Data = append(resp.Data, data)
		}
		// 形态 2: output.results = [{video_url, ...}]
		for _, r := range or.Output.Results {
			url := r.VideoURL
			if url == "" {
				url = r.URL // 兜底 — 部分 provider 用通用 url 字段
			}
			if url == "" {
				continue
			}
			data := provider.VideoData{
				URL:           url,
				CoverImageURL: r.CoverImageURL,
				DurationMs:    r.DurationMs,
			}
			if r.ActualPrompt != "" {
				data.RevisedPrompt = r.ActualPrompt
			}
			resp.Data = append(resp.Data, data)
		}
		if len(resp.Data) == 0 {
			return "failed", nil, fmt.Errorf("dashscope video: SUCCEEDED but no video_url (request_id=%s)", or.RequestID)
		}
		return "succeeded", resp, nil

	case "FAILED":
		code := or.Output.Code
		if code == "" {
			code = "UPSTREAM_FAILED"
		}
		msg := or.Output.Message
		if msg == "" {
			msg = "dashscope video task failed"
		}
		return "failed", nil, fmt.Errorf("dashscope video %s: %s", code, msg)

	default:
		return "running", nil, nil
	}
}

// ParseVideoResponse — 同步路径不走这里 (dashscope wanx-video 全 async).
// AsyncVideoAdaptor 接口要求实现, 兜底返 ErrNotImplemented.
func (a *Adaptor) ParseVideoResponse(body []byte) (*provider.VideoResponse, error) {
	return nil, fmt.Errorf("%w: dashscope video is async-only, use AsyncVideoAdaptor",
		provider.ErrNotImplemented)
}

// 编译期断言 — 同时满足 VideoAdaptor + AsyncVideoAdaptor.
var (
	_ provider.VideoAdaptor      = (*Adaptor)(nil)
	_ provider.AsyncVideoAdaptor = (*Adaptor)(nil)
)
