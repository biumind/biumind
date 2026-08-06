package dashscope

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

func TestTranslateImageRequest_BasicShape(t *testing.T) {
	a := New()
	req := &provider.ImageRequest{
		Model:  "wanx2.0-t2i-turbo",
		Prompt: "一只穿着宇航服的猫在月球上",
		Size:   "1024x1024", // OpenAI 风格 — 应被 normalize 成 *
		N:      2,
	}
	creds := &provider.Credentials{APIKey: "sk-test"}

	httpReq, err := a.TranslateImageRequest(context.Background(), req, creds)
	if err != nil {
		t.Fatalf("TranslateImageRequest: %v", err)
	}
	want := "https://dashscope.aliyuncs.com/api/v1/services/aigc/text2image/image-synthesis"
	if got := httpReq.URL.String(); got != want {
		t.Errorf("URL: %s, want %s", got, want)
	}
	if got := httpReq.Header.Get("X-DashScope-Async"); got != "enable" {
		t.Errorf("X-DashScope-Async: %q (必须 enable, 否则上游同步阻塞 30+s)", got)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: %q", got)
	}

	var body dashscopeImageRequest
	if err := json.NewDecoder(httpReq.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Model != "wanx2.0-t2i-turbo" {
		t.Errorf("model: %q", body.Model)
	}
	if body.Input.Prompt != "一只穿着宇航服的猫在月球上" {
		t.Errorf("input.prompt: %q", body.Input.Prompt)
	}
	if body.Parameters.Size != "1024*1024" {
		t.Errorf("parameters.size: %q (OpenAI x 应 normalize 成 dashscope *)", body.Parameters.Size)
	}
	if body.Parameters.N != 2 {
		t.Errorf("parameters.n: %d", body.Parameters.N)
	}
}

func TestTranslateImageRequest_DefaultN(t *testing.T) {
	a := New()
	req := &provider.ImageRequest{Model: "wanx", Prompt: "x"}
	httpReq, err := a.TranslateImageRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var body dashscopeImageRequest
	_ = json.NewDecoder(httpReq.Body).Decode(&body)
	if body.Parameters.N != 1 {
		t.Errorf("默认 n 应为 1, got %d", body.Parameters.N)
	}
}

func TestTranslateImageRequest_Errors(t *testing.T) {
	a := New()
	cases := []struct {
		name string
		req  *provider.ImageRequest
		cred *provider.Credentials
	}{
		{"missing key", &provider.ImageRequest{Prompt: "x"}, &provider.Credentials{APIKey: ""}},
		{"empty prompt", &provider.ImageRequest{Prompt: ""}, &provider.Credentials{APIKey: "sk"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.TranslateImageRequest(context.Background(), tc.req, tc.cred); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestParseImageSubmit_HappyPath(t *testing.T) {
	body := []byte(`{"output":{"task_id":"t-abc-123"},"request_id":"req_x"}`)
	a := New()
	id, err := a.ParseImageSubmit(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if id != "t-abc-123" {
		t.Errorf("task_id: %q", id)
	}
}

func TestParseImageSubmit_BusinessError(t *testing.T) {
	// 200 status 但 body 含 code/message → 业务错误
	body := []byte(`{"code":"InvalidApiKey","message":"key invalid","request_id":"req_x"}`)
	a := New()
	_, err := a.ParseImageSubmit(body)
	if err == nil || !strings.Contains(err.Error(), "InvalidApiKey") {
		t.Errorf("应识别 dashscope 业务错误: %v", err)
	}
}

func TestParseImageSubmit_MissingTaskID(t *testing.T) {
	body := []byte(`{"output":{},"request_id":"req_x"}`)
	a := New()
	_, err := a.ParseImageSubmit(body)
	if err == nil {
		t.Error("expected missing task_id error")
	}
}

func TestBuildPollRequest(t *testing.T) {
	a := New()
	httpReq, err := a.BuildPollRequest(context.Background(), "t-abc-123",
		&provider.Credentials{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := "https://dashscope.aliyuncs.com/api/v1/tasks/t-abc-123"
	if got := httpReq.URL.String(); got != want {
		t.Errorf("URL: %s, want %s", got, want)
	}
	if httpReq.Method != "GET" {
		t.Errorf("Method: %s", httpReq.Method)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: %q", got)
	}
}

func TestBuildPollRequest_Errors(t *testing.T) {
	a := New()
	if _, err := a.BuildPollRequest(context.Background(), "", &provider.Credentials{APIKey: "sk"}); err == nil {
		t.Error("empty task_id should error")
	}
	if _, err := a.BuildPollRequest(context.Background(), "x", &provider.Credentials{APIKey: ""}); err == nil {
		t.Error("missing key should error")
	}
}

func TestParsePollResponse_Running(t *testing.T) {
	cases := []string{"PENDING", "QUEUING", "RUNNING", "UNKNOWN"}
	a := New()
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			body := []byte(`{"output":{"task_status":"` + s + `"},"request_id":"x"}`)
			status, result, err := a.ParsePollResponse(body)
			if err != nil {
				t.Errorf("err: %v", err)
			}
			if status != "running" {
				t.Errorf("status: %q, want running", status)
			}
			if result != nil {
				t.Errorf("result should be nil while running")
			}
		})
	}
}

func TestParsePollResponse_Succeeded(t *testing.T) {
	body := []byte(`{
		"output": {
			"task_status": "SUCCEEDED",
			"results": [
				{"url":"https://oss.example.com/img1.png","actual_prompt":"a cat in space, high quality"},
				{"url":"https://oss.example.com/img2.png"}
			]
		},
		"request_id": "req_x"
	}`)
	a := New()
	status, result, err := a.ParsePollResponse(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if status != "succeeded" {
		t.Errorf("status: %q, want succeeded", status)
	}
	if result == nil || len(result.Data) != 2 {
		t.Fatalf("result.Data len: %v", result)
	}
	if result.Data[0].URL != "https://oss.example.com/img1.png" {
		t.Errorf("data[0].url: %q", result.Data[0].URL)
	}
	if result.Data[0].RevisedPrompt != "a cat in space, high quality" {
		t.Errorf("data[0].revised_prompt: %q (应透传 actual_prompt)", result.Data[0].RevisedPrompt)
	}
	// 没 actual_prompt 的 entry 应不带 revised_prompt
	if result.Data[1].RevisedPrompt != "" {
		t.Errorf("data[1].revised_prompt should be empty, got %q", result.Data[1].RevisedPrompt)
	}
}

func TestParsePollResponse_SucceededButEmpty(t *testing.T) {
	// SUCCEEDED 但 results 数组空 — 当 failed
	body := []byte(`{"output":{"task_status":"SUCCEEDED","results":[]},"request_id":"x"}`)
	a := New()
	status, result, err := a.ParsePollResponse(body)
	if status != "failed" || err == nil || result != nil {
		t.Errorf("SUCCEEDED+empty results should be failed: status=%q err=%v result=%v",
			status, err, result)
	}
}

func TestParsePollResponse_Failed(t *testing.T) {
	body := []byte(`{
		"output": {"task_status":"FAILED","code":"InternalError","message":"内部异常"},
		"request_id":"x"
	}`)
	a := New()
	status, result, err := a.ParsePollResponse(body)
	if status != "failed" {
		t.Errorf("status: %q, want failed", status)
	}
	if err == nil || !strings.Contains(err.Error(), "InternalError") {
		t.Errorf("err 应包含 dashscope code: %v", err)
	}
	if result != nil {
		t.Error("failed 时 result 应 nil")
	}
}

func TestParseImageResponse_NotImplemented(t *testing.T) {
	a := New()
	_, err := a.ParseImageResponse([]byte(`{}`))
	if !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("dashscope sync image path 应返 ErrNotImplemented, got %v", err)
	}
}
