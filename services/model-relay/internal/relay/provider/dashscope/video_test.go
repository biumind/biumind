package dashscope

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

func TestTranslateVideoRequest_BasicShape(t *testing.T) {
	a := New()
	req := &provider.VideoRequest{
		Model:           "wanx2.1-i2v-turbo",
		Prompt:          "宇航员太空漂浮的延时镜头",
		Size:            "1280x720", // OpenAI 风格 — 应被 normalize 成 *
		DurationSeconds: 5,
	}
	creds := &provider.Credentials{APIKey: "sk-test"}

	httpReq, err := a.TranslateVideoRequest(context.Background(), req, creds)
	if err != nil {
		t.Fatalf("TranslateVideoRequest: %v", err)
	}
	want := "https://dashscope.aliyuncs.com/api/v1/services/aigc/video-generation/video-synthesis"
	if got := httpReq.URL.String(); got != want {
		t.Errorf("URL: %s, want %s", got, want)
	}
	if got := httpReq.Header.Get("X-DashScope-Async"); got != "enable" {
		t.Errorf("X-DashScope-Async: %q (必须 enable)", got)
	}

	var body dashscopeVideoRequest
	if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Model != "wanx2.1-i2v-turbo" {
		t.Errorf("model: %q", body.Model)
	}
	if body.Input.Prompt != "宇航员太空漂浮的延时镜头" {
		t.Errorf("input.prompt: %q", body.Input.Prompt)
	}
	if body.Parameters.Size != "1280*720" {
		t.Errorf("parameters.size: %q (x 应 normalize 成 *)", body.Parameters.Size)
	}
	if body.Parameters.Duration != 5 {
		t.Errorf("parameters.duration: %d", body.Parameters.Duration)
	}
	if !body.Parameters.PromptExtend {
		t.Error("parameters.prompt_extend 默认应为 true")
	}
}

func TestTranslateVideoRequest_FirstFrameOnly(t *testing.T) {
	// 单首帧 → DashScope 老字段 img_url
	a := New()
	req := &provider.VideoRequest{
		Model: "wanx2.1-i2v-turbo", Prompt: "延伸这张图",
		FirstFrameURL: "https://example.com/frame1.png",
	}
	httpReq, _ := a.TranslateVideoRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk"})
	var body dashscopeVideoRequest
	_ = json.NewDecoder(httpReq.Body).Decode(&body)
	if body.Input.ImgURL != "https://example.com/frame1.png" {
		t.Errorf("仅首帧应填 img_url: got %q", body.Input.ImgURL)
	}
	if body.Input.FirstFrameURL != "" || body.Input.LastFrameURL != "" {
		t.Errorf("仅首帧不该填 first_frame_url/last_frame_url: %+v", body.Input)
	}
}

func TestTranslateVideoRequest_FirstAndLastFrame(t *testing.T) {
	// 首+尾帧 → first_frame_url + last_frame_url 新字段
	a := New()
	req := &provider.VideoRequest{
		Model: "wanx2.1-i2v-turbo", Prompt: "从 A 过渡到 B",
		FirstFrameURL: "https://example.com/a.png",
		LastFrameURL:  "https://example.com/b.png",
	}
	httpReq, _ := a.TranslateVideoRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk"})
	var body dashscopeVideoRequest
	_ = json.NewDecoder(httpReq.Body).Decode(&body)
	if body.Input.FirstFrameURL == "" || body.Input.LastFrameURL == "" {
		t.Errorf("首尾帧字段缺失: %+v", body.Input)
	}
	if body.Input.ImgURL != "" {
		t.Errorf("首+尾时不该填 img_url, got %q", body.Input.ImgURL)
	}
}

func TestTranslateVideoRequest_AspectToSize(t *testing.T) {
	// AspectRatio + Resolution → 查表得 Size
	a := New()
	cases := []struct {
		aspect, res, wantSize string
	}{
		{"16:9", "720p", "1280*720"},
		{"16:9", "1080p", "1920*1080"},
		{"9:16", "720p", "720*1280"},
		{"1:1", "1080p", "1080*1080"},
		{"16:9", "", "1280*720"}, // 默认 720p
		{"unknown", "720p", ""},  // 不识别 → 空字符串让上游用默认
	}
	for _, tc := range cases {
		t.Run(tc.aspect+"_"+tc.res, func(t *testing.T) {
			req := &provider.VideoRequest{
				Model: "x", Prompt: "y",
				AspectRatio: tc.aspect, Resolution: tc.res,
			}
			httpReq, _ := a.TranslateVideoRequest(context.Background(), req,
				&provider.Credentials{APIKey: "sk"})
			var body dashscopeVideoRequest
			_ = json.NewDecoder(httpReq.Body).Decode(&body)
			if body.Parameters.Size != tc.wantSize {
				t.Errorf("size: %q, want %q", body.Parameters.Size, tc.wantSize)
			}
		})
	}
}

func TestTranslateVideoRequest_Errors(t *testing.T) {
	a := New()
	cases := []struct {
		name string
		req  *provider.VideoRequest
		cred *provider.Credentials
	}{
		{"missing key", &provider.VideoRequest{Prompt: "x"}, &provider.Credentials{APIKey: ""}},
		{"empty prompt", &provider.VideoRequest{}, &provider.Credentials{APIKey: "sk"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.TranslateVideoRequest(context.Background(), tc.req, tc.cred); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseVideoSubmit_HappyPath(t *testing.T) {
	body := []byte(`{"output":{"task_id":"v-task-123"},"request_id":"req"}`)
	a := New()
	id, err := a.ParseVideoSubmit(body)
	if err != nil || id != "v-task-123" {
		t.Errorf("submit: id=%q err=%v", id, err)
	}
}

func TestParseVideoSubmit_BusinessError(t *testing.T) {
	body := []byte(`{"code":"InvalidParameter","message":"bad","request_id":"r"}`)
	a := New()
	_, err := a.ParseVideoSubmit(body)
	if err == nil || !strings.Contains(err.Error(), "InvalidParameter") {
		t.Errorf("err: %v", err)
	}
}

func TestBuildVideoPollRequest(t *testing.T) {
	a := New()
	httpReq, err := a.BuildVideoPollRequest(context.Background(), "v-task-123",
		&provider.Credentials{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := "https://dashscope.aliyuncs.com/api/v1/tasks/v-task-123"
	if got := httpReq.URL.String(); got != want {
		t.Errorf("URL: %s, want %s (poll path 跟 image 共用)", got, want)
	}
	if httpReq.Method != "GET" {
		t.Errorf("Method: %s", httpReq.Method)
	}
}

func TestParseVideoPollResponse_Running(t *testing.T) {
	cases := []string{"PENDING", "QUEUING", "RUNNING", "UNKNOWN"}
	a := New()
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			body := []byte(`{"output":{"task_status":"` + s + `"}}`)
			status, result, err := a.ParseVideoPollResponse(body)
			if err != nil || status != "running" || result != nil {
				t.Errorf("status=%q result=%v err=%v, want running/nil/nil",
					status, result, err)
			}
		})
	}
}

func TestParseVideoPollResponse_SucceededDirectFields(t *testing.T) {
	// 老形态: output.video_url 直接挂 output 上
	body := []byte(`{
		"output": {
			"task_status": "SUCCEEDED",
			"video_url": "https://oss.example.com/v1.mp4",
			"cover_image_url": "https://oss.example.com/v1_cover.jpg",
			"actual_prompt": "一段被 LLM 改写的 prompt"
		},
		"request_id": "req_x"
	}`)
	a := New()
	status, result, err := a.ParseVideoPollResponse(body)
	if err != nil || status != "succeeded" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	if len(result.Data) != 1 {
		t.Fatalf("data len: %d", len(result.Data))
	}
	if result.Data[0].URL != "https://oss.example.com/v1.mp4" {
		t.Errorf("url: %q", result.Data[0].URL)
	}
	if result.Data[0].CoverImageURL != "https://oss.example.com/v1_cover.jpg" {
		t.Errorf("cover: %q", result.Data[0].CoverImageURL)
	}
	if result.Data[0].RevisedPrompt == "" {
		t.Error("revised_prompt 应透传 actual_prompt")
	}
}

func TestParseVideoPollResponse_SucceededResultsArray(t *testing.T) {
	// 新形态: output.results 数组
	body := []byte(`{
		"output": {
			"task_status": "SUCCEEDED",
			"results": [
				{"video_url":"https://oss.example.com/v1.mp4","cover_image_url":"https://oss.example.com/v1_c.jpg","duration_ms":5000},
				{"video_url":"https://oss.example.com/v2.mp4"}
			]
		},
		"request_id": "req_x"
	}`)
	a := New()
	status, result, err := a.ParseVideoPollResponse(body)
	if err != nil || status != "succeeded" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	if len(result.Data) != 2 {
		t.Fatalf("data len: %d", len(result.Data))
	}
	if result.Data[0].DurationMs != 5000 {
		t.Errorf("duration_ms: %d", result.Data[0].DurationMs)
	}
}

func TestParseVideoPollResponse_SucceededEmpty(t *testing.T) {
	// SUCCEEDED 但 results 空 + 无 video_url — 当 failed
	body := []byte(`{"output":{"task_status":"SUCCEEDED","results":[]}}`)
	a := New()
	status, _, err := a.ParseVideoPollResponse(body)
	if status != "failed" || err == nil {
		t.Errorf("空 results 应当 failed: status=%q err=%v", status, err)
	}
}

func TestParseVideoPollResponse_Failed(t *testing.T) {
	body := []byte(`{"output":{"task_status":"FAILED","code":"DataInspectionFailed","message":"内容审核未过"}}`)
	a := New()
	status, _, err := a.ParseVideoPollResponse(body)
	if status != "failed" {
		t.Errorf("status: %q", status)
	}
	if err == nil || !strings.Contains(err.Error(), "DataInspectionFailed") {
		t.Errorf("err 应包含上游错误码: %v", err)
	}
}

func TestParseVideoResponse_NotImplemented(t *testing.T) {
	a := New()
	_, err := a.ParseVideoResponse([]byte(`{}`))
	if !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("dashscope sync video 应返 ErrNotImplemented, got %v", err)
	}
}
