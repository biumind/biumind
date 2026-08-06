// transcribe.go — openai.Adaptor 实现 provider.TranscribeAdaptor (v0.3 M6).
//
// OpenAI Whisper / GPT-4o-transcribe 走 multipart/form-data:
//   POST {base}/v1/audio/transcriptions
//   Headers: Authorization: Bearer {key}, Content-Type: multipart/form-data
//   Form fields:
//     file:            <audio bytes> (mp3/wav/m4a/webm/flac/ogg)
//     model:           "whisper-1" / "gpt-4o-transcribe" 等
//     language:        "zh" / "en" (可选, 不传 = auto detect)
//     prompt:          引导风格 (可选)
//     response_format: "json" / "text" / "srt" / "verbose_json" / "vtt"
//     temperature:     0.0
//
// Response (默认 json 模式):
//   {"text": "..."}
//
// verbose_json 模式额外字段:
//   {"text", "language", "duration", "words":[...], "segments":[...]}
//
// SiliconFlow / 自部署 faster-whisper / Groq 等 OpenAI-compat 上游全部
// 走同形态.

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// TranslateTranscribeRequest 构造 multipart 上游请求.
func (a *Adaptor) TranslateTranscribeRequest(
	ctx context.Context, req *provider.TranscribeRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("openai: missing API key")
	}
	if req.Audio == nil {
		return nil, fmt.Errorf("openai: empty audio")
	}
	filename := req.AudioFilename
	if filename == "" {
		filename = "audio.mp3" // 不传文件名时给个默认, 上游靠 ext 识别格式
	}

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	// file part — 必填
	fileWriter, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("openai: multipart file: %w", err)
	}
	if _, err := io.Copy(fileWriter, req.Audio); err != nil {
		return nil, fmt.Errorf("openai: copy audio: %w", err)
	}

	// model 必填
	if err := mw.WriteField("model", req.Model); err != nil {
		return nil, err
	}
	// 可选字段, 空就不写让上游用默认
	if req.Language != "" {
		_ = mw.WriteField("language", req.Language)
	}
	if req.Prompt != "" {
		_ = mw.WriteField("prompt", req.Prompt)
	}
	if req.ResponseFormat != "" {
		_ = mw.WriteField("response_format", req.ResponseFormat)
	}
	if req.Temperature > 0 {
		_ = mw.WriteField("temperature", strconv.FormatFloat(req.Temperature, 'f', 2, 64))
	}

	if err := mw.Close(); err != nil {
		return nil, err
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = creds.BaseURL
	}
	base = provider.NormalizeBaseURL(base)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v1/audio/transcriptions", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	if creds.Extra["organization"] != "" {
		httpReq.Header.Set("OpenAI-Organization", creds.Extra["organization"])
	}
	if creds.Extra["project"] != "" {
		httpReq.Header.Set("OpenAI-Project", creds.Extra["project"])
	}
	return httpReq, nil
}

// ParseTranscribeResponse — 同时支持 json / verbose_json 两种 OpenAI 响应.
//   text 模式上游会直接返 plain text, 不是 JSON — caller 应在 handler 层
//   先看 Content-Type 判定; ParseTranscribeResponse 假定收到 JSON.
func (a *Adaptor) ParseTranscribeResponse(body []byte) (*provider.TranscribeResponse, error) {
	var or struct {
		Text     string  `json:"text"`
		Language string  `json:"language,omitempty"`
		Duration float64 `json:"duration,omitempty"`
		Words    []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words,omitempty"`
		Segments []struct {
			ID    int     `json:"id"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments,omitempty"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return nil, fmt.Errorf("openai: parse transcribe: %w", err)
	}
	out := &provider.TranscribeResponse{
		Text:     or.Text,
		Language: or.Language,
		Duration: or.Duration,
	}
	for _, w := range or.Words {
		out.Words = append(out.Words, provider.TranscribedWord{
			Word: w.Word, Start: w.Start, End: w.End,
		})
	}
	for _, s := range or.Segments {
		out.Segments = append(out.Segments, provider.TranscribedSeg{
			ID: s.ID, Start: s.Start, End: s.End, Text: s.Text,
		})
	}
	return out, nil
}

// 编译期断言.
var _ provider.TranscribeAdaptor = (*Adaptor)(nil)
