// dashscope.Adaptor — cosyvoice TTS via 阿里云 DashScope 非实时 HTTP API.
//
// 端点 (北京地域 only, 国际/新加坡 该端点不可用):
//   POST https://dashscope.aliyuncs.com/api/v1/services/audio/tts/SpeechSynthesizer
//   Authorization: Bearer <api-key>
//   Content-Type:  application/json
//   X-DashScope-SSE: enable                          ← 流式开关
//
// 请求 body (示例 cosyvoice-v3-flash 系统音色 longanyang):
//   {
//     "model": "cosyvoice-v3-flash",
//     "input": {
//       "text":         "...",          // 待合成文本
//       "voice":        "longanyang",   // 系统音色 / 复刻音色 ID
//       "format":       "mp3",          // mp3/pcm/wav/opus, default mp3
//       "sample_rate":  22050,          // 8000/16000/22050/24000/44100/48000
//       "volume":       50,             // [0,100]   default 50
//       "rate":         1.0,            // [0.5,2]  default 1
//       "pitch":        1.0,            // [0.5,2]  default 1
//       ...
//     }
//   }
//
// 响应:
//   非流式 (SSE 关闭):  一次性 JSON, output.audio.url 是预签 OSS 链接 (24h),
//                       output.audio.data 为空字符串. 我们当前永远走流式以
//                       拿 inline base64, 避免再一跳 OSS 拉取.
//   流式 (SSE 开):     若干 data: {...}\n\n 帧:
//                       output.type ∈ {sentence-begin, sentence-synthesis, sentence-end}
//                       output.audio.data: base64 PCM/MP3 bytes 块
//                       output.finish_reason == "stop" 表示最后一帧
//
// WebSocket 实时合成路径 (cosyvoice-v3.5-flash 等只支持 WS) 在 M5 实现; M1
// OpenSpeechWebSocket 返 ErrNotImplemented.
//
// TranscribeAdaptor (paraformer ASR) 在 M3 加, 当前不实现 → 类型断言失败.

package dashscope

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

const (
	defaultBaseURL = "https://dashscope.aliyuncs.com"
	speechPath     = "/api/v1/services/audio/tts/SpeechSynthesizer"
)

// Adaptor 实现 provider.SpeechAdaptor (cosyvoice). 后续 M3 在此基础上加
// TranscribeAdaptor / ImageAdaptor 方法即可让 type assertion 命中更多
// modality.
type Adaptor struct{}

func New() *Adaptor { return &Adaptor{} }

func (a *Adaptor) Name() string { return "dashscope" }

// Capabilities —
//
//	M1:    audio_speech (cosyvoice TTS, audio.go)
//	M2.5:  rerank (gte-rerank-v2 / qwen3-rerank, rerank.go)
//	M3:    image_generation (wanx-* / qwen-image, image.go async submit+poll)
//	M4:    video_generation (wanx-video / wanx2.1-i2v-turbo, video.go async submit+poll)
//	M6.5:  audio_transcription (paraformer-v2 / sensevoice, transcribe.go async)
//
// 加 modality 时同时:
//  1. 追加方法实现对应 interface
//  2. 在这里加 capability 字符串 (用 registry.Mode* 常量值)
//  3. 编译期断言 (各 file 末尾的 var _ provider.XxxAdaptor)
func (a *Adaptor) Capabilities() []string {
	return []string{"audio_speech", "rerank", "image_generation",
		"video_generation", "audio_transcription"}
}

// ─── Speech (TTS) ─────────────────────────────────────────────────────

// dashscopeSpeechRequest 是上游 body shape. 字段名严格对齐文档.
type dashscopeSpeechRequest struct {
	Model string                   `json:"model"`
	Input dashscopeSpeechInputBody `json:"input"`
}

type dashscopeSpeechInputBody struct {
	Text                 string   `json:"text"`
	Voice                string   `json:"voice"`
	Format               string   `json:"format,omitempty"`
	SampleRate           int      `json:"sample_rate,omitempty"`
	Volume               int      `json:"volume,omitempty"`
	Rate                 float64  `json:"rate,omitempty"`
	Pitch                float64  `json:"pitch,omitempty"`
	BitRate              int      `json:"bit_rate,omitempty"`
	EnableSSML           bool     `json:"enable_ssml,omitempty"`
	WordTimestampEnabled bool     `json:"word_timestamp_enabled,omitempty"`
	Seed                 int      `json:"seed,omitempty"`
	LanguageHints        []string `json:"language_hints,omitempty"`
	Instruction          string   `json:"instruction,omitempty"`
	EnableMarkdownFilter bool     `json:"enable_markdown_filter,omitempty"`
}

// TranslateSpeechRequest — 永远启用 SSE (X-DashScope-SSE: enable). 即便调用方
// 不要流式, handler 也可以 buffer channel 输出再一次性 dump; 这避免非流式
// 路径多一跳 OSS URL 拉取.
func (a *Adaptor) TranslateSpeechRequest(
	ctx context.Context, req *provider.SpeechRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("dashscope: missing API key")
	}
	if req.Input == "" {
		return nil, fmt.Errorf("dashscope: empty input text")
	}
	if req.Voice == "" {
		return nil, fmt.Errorf("dashscope: voice required (cosyvoice has no default)")
	}

	body := dashscopeSpeechRequest{
		Model: req.Model,
		Input: dashscopeSpeechInputBody{
			Text:       req.Input,
			Voice:      req.Voice,
			Format:     normalizeFormat(req.ResponseFormat),
			SampleRate: req.SampleRate,
		},
	}
	if req.Speed > 0 {
		body.Input.Rate = req.Speed
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("dashscope: marshal: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+speechPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-DashScope-SSE", "enable")
	return httpReq, nil
}

// dashscopeSpeechSSEEvent — SSE data: {...} 一帧.
// 只解析我们需要的字段 (audio.data + finish_reason + type).
type dashscopeSpeechSSEEvent struct {
	RequestID string `json:"request_id"`
	Output    struct {
		FinishReason string `json:"finish_reason"`
		Type         string `json:"type"`
		Audio        struct {
			Data      string `json:"data"`
			ID        string `json:"id"`
			URL       string `json:"url,omitempty"`
			ExpiresAt int64  `json:"expires_at,omitempty"`
		} `json:"audio"`
	} `json:"output"`
	Usage struct {
		Characters int `json:"characters"`
	} `json:"usage"`
}

// StreamAudioFrames 解析 SSE, 每个 data: {...} 帧解 base64 → AudioFrame.
// finish_reason=="stop" 那一帧 Final=true.
//
// 错误处理:
//   - SSE 解析失败 (格式错乱 / 上游返 application/json 错误体): 把错误内容
//     当成单一 error frame 透出, channel close.
//   - base64 decode 失败: 跳过该帧 (日志由上层加, 这里静默 — adaptor 层
//     不持有 logger).
func (a *Adaptor) StreamAudioFrames(
	ctx context.Context, body interface{ Read([]byte) (int, error) },
) (<-chan provider.AudioFrame, error) {
	out := make(chan provider.AudioFrame, 16)

	mimeType := "audio/mpeg" // 默认 mp3
	go func() {
		defer close(out)
		sc := bufio.NewScanner(body)
		// SSE 一行最长 ~ 几十 KB (base64 块), 给 1MB 充足 buffer.
		buf := make([]byte, 0, 1024*1024)
		sc.Buffer(buf, cap(buf))

		for sc.Scan() {
			line := sc.Text()
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var ev dashscopeSpeechSSEEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				// 上游返非 SSE shape 的错误体 (如 voice 不合法时常见的
				// {"code":"InvalidParameter","message":"..."}). 把原始 body
				// 透出给 handler, 让 handler 决定 502 + 真错误信息, 而不是
				// 静默吞掉变 200 + 0 字节.
				select {
				case out <- provider.AudioFrame{
					Err: fmt.Errorf("upstream non-SSE response: %s",
						truncate(data, 300)),
					ReceivedAt: time.Now(),
				}:
				case <-ctx.Done():
				}
				return
			}
			if ev.Output.Audio.Data != "" {
				raw, derr := base64.StdEncoding.DecodeString(ev.Output.Audio.Data)
				if derr != nil || len(raw) == 0 {
					continue
				}
				select {
				case out <- provider.AudioFrame{
					Data:       raw,
					MimeType:   mimeType,
					Final:      ev.Output.FinishReason == "stop",
					ReceivedAt: time.Now(),
					// M5: 计费维度. dashscope 在每帧 usage.characters 都透传
					// (cumulative), 最终帧拿到的是总数. handler 累计取最后值.
					Characters: ev.Usage.Characters,
				}:
				case <-ctx.Done():
					return
				}
			}
			if ev.Output.FinishReason == "stop" {
				// M5: stop 帧可能没 audio data 但有 usage.characters,
				// 需要补一个空 Data 的 Final 帧让 handler 拿到总 chars.
				if ev.Usage.Characters > 0 && ev.Output.Audio.Data == "" {
					select {
					case out <- provider.AudioFrame{
						MimeType:   mimeType,
						Final:      true,
						ReceivedAt: time.Now(),
						Characters: ev.Usage.Characters,
					}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
		// scanner 自然终止 (EOF) — 不视作错误.
	}()

	return out, nil
}

// OpenSpeechWebSocket — cosyvoice-v3.5-flash 等纯 WS 模型走这里. M5 实现
// run-task / continue-task / finish-task duplex 协议; M1 返 ErrNotImplemented.
func (a *Adaptor) OpenSpeechWebSocket(
	ctx context.Context, req *provider.SpeechRequest, creds *provider.Credentials,
) (provider.WebSocketSession, error) {
	return nil, provider.ErrNotImplemented
}

// normalizeFormat — 把客户端传的 format 兜底到 dashscope 接受的形态.
// 默认 mp3; 不识别的返空字符串让上游用它的默认值 (避免误传 400).
// truncate — 错误体可能很长 (HTML / 大 JSON), 给 logger / error 截断成
// 可读片段. 不是 utf8-safe, 但只用于 log/error 字符串, 截在多字节中间也
// 只是少看几个字符, 不影响诊断.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func normalizeFormat(f string) string {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "mp3", "":
		return "mp3"
	case "pcm":
		return "pcm"
	case "wav":
		return "wav"
	case "opus":
		return "opus"
	default:
		return ""
	}
}

// 编译期断言 — adaptor 必须满足 SpeechAdaptor. 不实现 chat Adaptor 是
// 故意的: 让 ChatAdaptor type assertion 失败, 守住 dashscope 的 chat 模型
// 只能走 protocol=openai_compat → openai adaptor.
var _ provider.SpeechAdaptor = (*Adaptor)(nil)

// (M3 加 TranscribeAdaptor / ImageAdaptor 实现后, 在这里加编译期断言.)
//   var _ provider.TranscribeAdaptor = (*Adaptor)(nil)   // M3
//   var _ provider.ImageAdaptor = (*Adaptor)(nil)        // M3
