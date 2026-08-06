// video.go — VolcEngine Seedance 文生视频 (异步 VideoAdaptor + AsyncVideoAdaptor).
//
// Ark 视频是 task 异步模式(与 dashscope 视频对齐):
//   submit: POST {base}/contents/generations/tasks
//           Body: {"model","content":[{type:text,text},{type:image_url,...,role}],
//                  "ratio","resolution","duration","generate_audio"}
//           Resp: {"id":"<task_id>"}
//   poll:   GET {base}/contents/generations/tasks/{id}
//           Resp: {"id","status":queued|running|succeeded|failed|expired|cancelled,
//                  "content":{"video_url","last_frame_url"}, "error":{...}}
//
// 移植自 workers/aigc/biumind_aigc/providers/volcengine_video.py。

package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

const videoTasksPath = "/contents/generations/tasks"

type arkContentEntry struct {
	Type     string `json:"type"` // "text" | "image_url"
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
	Role string `json:"role,omitempty"` // first_frame | last_frame | reference_image
}

type arkVideoRequest struct {
	Model         string            `json:"model"`
	Content       []arkContentEntry `json:"content"`
	Ratio         string            `json:"ratio,omitempty"`
	Resolution    string            `json:"resolution,omitempty"`
	Duration      int               `json:"duration,omitempty"`
	GenerateAudio bool              `json:"generate_audio,omitempty"`
}

func imageURLEntry(url, role string) arkContentEntry {
	e := arkContentEntry{Type: "image_url"}
	e.ImageURL = &struct {
		URL string `json:"url"`
	}{URL: url}
	e.Role = role
	return e
}

// TranslateVideoRequest 构造 Ark 视频 submit 请求。
func (a *Adaptor) TranslateVideoRequest(
	ctx context.Context, req *provider.VideoRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("volcengine: missing API key")
	}
	if req.Prompt == "" {
		return nil, fmt.Errorf("volcengine: empty prompt")
	}

	content := []arkContentEntry{{Type: "text", Text: req.Prompt}}
	// 有 last_frame 时 first_frame 必须标 role;否则首帧不标 role。
	if req.FirstFrameURL != "" {
		role := ""
		if req.LastFrameURL != "" {
			role = "first_frame"
		}
		content = append(content, imageURLEntry(req.FirstFrameURL, role))
	}
	if req.LastFrameURL != "" {
		content = append(content, imageURLEntry(req.LastFrameURL, "last_frame"))
	}
	for _, ref := range req.ReferenceImageURLs {
		if ref == "" {
			continue
		}
		content = append(content, imageURLEntry(ref, "reference_image"))
	}

	body := arkVideoRequest{
		Model:      req.Model,
		Content:    content,
		Ratio:      req.AspectRatio,
		Resolution: strings.ToLower(req.Resolution),
		Duration:   req.DurationSeconds,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("volcengine: marshal video: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+videoTasksPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

// ParseVideoResponse — 同步路径不走(Ark 视频全 async)。
func (a *Adaptor) ParseVideoResponse(body []byte) (*provider.VideoResponse, error) {
	return nil, fmt.Errorf("%w: volcengine video is async-only, use AsyncVideoAdaptor",
		provider.ErrNotImplemented)
}

// ParseVideoSubmit — submit 响应解 task id。
func (a *Adaptor) ParseVideoSubmit(body []byte) (string, error) {
	var sr struct {
		ID    string `json:"id"`
		Error struct {
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &sr); err != nil {
		return "", fmt.Errorf("volcengine: parse video submit: %w", err)
	}
	if sr.ID == "" {
		if sr.Error.Code != "" || sr.Error.Message != "" {
			return "", fmt.Errorf("volcengine video submit %s: %s", sr.Error.Code, sr.Error.Message)
		}
		return "", fmt.Errorf("volcengine: video submit missing id")
	}
	return sr.ID, nil
}

// BuildVideoPollRequest — GET /contents/generations/tasks/{id}。
func (a *Adaptor) BuildVideoPollRequest(
	ctx context.Context, taskID string, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("volcengine: missing API key")
	}
	if taskID == "" {
		return nil, fmt.Errorf("volcengine: empty task_id")
	}
	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+videoTasksPath+"/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	return httpReq, nil
}

// ParseVideoPollResponse — 规范化 Ark 状态。
//
//	queued/pending/running → "running"
//	succeeded              → "succeeded" + VideoResponse(video_url + last_frame 作 cover)
//	failed/expired/cancelled → "failed" + err
func (a *Adaptor) ParseVideoPollResponse(body []byte) (string, *provider.VideoResponse, error) {
	var pr struct {
		Status  string `json:"status"`
		Content struct {
			VideoURL     string `json:"video_url,omitempty"`
			LastFrameURL string `json:"last_frame_url,omitempty"`
		} `json:"content"`
		Error struct {
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", nil, fmt.Errorf("volcengine: parse video poll: %w", err)
	}

	switch strings.ToLower(pr.Status) {
	case "queued", "pending", "running", "":
		return "running", nil, nil
	case "succeeded":
		if pr.Content.VideoURL == "" {
			return "failed", nil, fmt.Errorf("volcengine: succeeded but no video_url")
		}
		resp := &provider.VideoResponse{
			Data: []provider.VideoData{{
				URL:           pr.Content.VideoURL,
				CoverImageURL: pr.Content.LastFrameURL,
			}},
		}
		return "succeeded", resp, nil
	case "failed", "expired", "cancelled":
		code := pr.Error.Code
		if code == "" {
			code = strings.ToUpper(pr.Status)
		}
		msg := pr.Error.Message
		if msg == "" {
			msg = "volcengine video " + pr.Status
		}
		return "failed", nil, fmt.Errorf("volcengine %s: %s", code, msg)
	default:
		return "running", nil, nil
	}
}

// 编译期断言 — VideoAdaptor + AsyncVideoAdaptor。
var (
	_ provider.VideoAdaptor      = (*Adaptor)(nil)
	_ provider.AsyncVideoAdaptor = (*Adaptor)(nil)
)
