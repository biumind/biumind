package dashscope

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// qwen3-asr-flash 同步路径: 音频字节 → base64 内联 multimodal 请求。
func TestTranslateTranscribe_QwenASRSync(t *testing.T) {
	a := New()
	req := &provider.TranscribeRequest{
		Model:         "qwen3-asr-flash",
		Audio:         strings.NewReader("FAKEWAVBYTES"),
		AudioFilename: "rec.wav",
	}
	httpReq, err := a.TranslateTranscribeRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.HasSuffix(httpReq.URL.String(), multimodalGenPath) {
		t.Errorf("URL: %s, want suffix %s", httpReq.URL.String(), multimodalGenPath)
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("auth: %q", got)
	}
	// 同步路径不应带 async header。
	if httpReq.Header.Get("X-DashScope-Async") != "" {
		t.Error("qwen-asr 同步路径不应有 X-DashScope-Async")
	}
	body, _ := io.ReadAll(httpReq.Body)
	var qr qwenASRRequest
	if err := json.Unmarshal(body, &qr); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if qr.Model != "qwen3-asr-flash" {
		t.Errorf("model: %q", qr.Model)
	}
	if len(qr.Input.Messages) != 1 || len(qr.Input.Messages[0].Content) != 1 {
		t.Fatalf("messages shape: %+v", qr.Input.Messages)
	}
	audio := qr.Input.Messages[0].Content[0]["audio"]
	if !strings.HasPrefix(audio, "data:audio/wav;base64,") {
		t.Errorf("audio data URI: %q", audio[:min(40, len(audio))])
	}
}

func TestTranslateTranscribe_RoutesAsyncWhenFileURLs(t *testing.T) {
	a := New()
	// 有 file_urls → 走 paraformer 异步 submit (带 async header)。
	req := &provider.TranscribeRequest{
		Model:    "paraformer-v2",
		FileURLs: []string{"https://x/a.wav"},
	}
	httpReq, err := a.TranslateTranscribeRequest(context.Background(), req,
		&provider.Credentials{APIKey: "sk"})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.HasSuffix(httpReq.URL.String(), transcribeSubmitPath) {
		t.Errorf("URL: %s, want async submit path", httpReq.URL.String())
	}
	if httpReq.Header.Get("X-DashScope-Async") != "enable" {
		t.Error("paraformer 异步路径应有 X-DashScope-Async: enable")
	}
}

// 用实测捕获的真实 qwen3-asr-flash 响应 JSON 验证解析。
func TestParseTranscribeResponse_QwenASR(t *testing.T) {
	a := New()
	// 这是真实调阿里云 qwen3-asr-flash 拿到的响应 (含 annotations)。
	real := `{"output":{"choices":[{"finish_reason":"stop","message":{"annotations":[{"emotion":"neutral","language":"zh","type":"audio_info"}],"content":[{"text":"hello world，这里是阿里巴巴语音实验室。"}],"role":"assistant"}}]},"usage":{"audio_tokens":95}}`
	resp, err := a.ParseTranscribeResponse([]byte(real))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Text != "hello world，这里是阿里巴巴语音实验室。" {
		t.Errorf("text: %q", resp.Text)
	}
	if resp.Language != "zh" {
		t.Errorf("language: %q, want zh", resp.Language)
	}
}

func TestParseTranscribeResponse_Error(t *testing.T) {
	a := New()
	_, err := a.ParseTranscribeResponse([]byte(`{"code":"InvalidParameter","message":"bad audio"}`))
	if err == nil {
		t.Error("code 非空应返错")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
