// transcribe.go — dashscope.Adaptor 实现 provider.AsyncTranscribeAdaptor (M6.5).
//
// 阿里云百炼 paraformer-v2 / sensevoice 是 file_url 异步模式:
//
//   submit:  POST {base}/api/v1/services/audio/asr/transcription
//            Headers: Authorization: Bearer / X-DashScope-Async: enable
//            Body: {
//              "model":"paraformer-v2",
//              "input":{"file_urls":["https://..."]},
//              "parameters":{"language_hints":["zh","en"]}
//            }
//            Resp: {"output":{"task_id":"..."}}
//
//   poll:    GET {base}/api/v1/tasks/{task_id}
//            SUCCEEDED 时 results 数组每项是一个 file 的转写状态:
//              {"file_url":"...", "transcription_url":"...",
//               "subtask_status":"SUCCEEDED"}
//            transcription_url 必须二次 fetch 才能拿到真正的 ASR JSON.
//
//   fetch:   GET {transcription_url}   (公开 OSS, 不需鉴权)
//            Resp: {
//              "file_url":"...",
//              "properties":{...},
//              "transcripts":[{
//                "channel_id":0,
//                "content_duration_in_milliseconds":30000,
//                "text":"完整的转写文本",
//                "sentences":[{"begin_time":0,"end_time":1500,"text":"...","words":[...]}]
//              }]
//            }
//
// 与 workers/aigc 没有现成实现 — 这是 model-relay 第一个 ASR 接入.

package dashscope

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

const (
	transcribeSubmitPath = "/api/v1/services/audio/asr/transcription"
	// qwen3-asr-flash 等 Qwen ASR 走多模态生成端点 (同步): 音频以 URL 或
	// data:audio/<fmt>;base64,<...> 内联放进 messages content。实测确认
	// base64 内联可用 → 客户端录音无需先上传拿公网 URL。
	multimodalGenPath = "/api/v1/services/aigc/multimodal-generation/generation"
)

// dashscopeTranscribeRequest — 上游 wire shape.
type dashscopeTranscribeRequest struct {
	Model      string                        `json:"model"`
	Input      dashscopeTranscribeInputBody  `json:"input"`
	Parameters dashscopeTranscribeParameters `json:"parameters,omitempty"`
}

type dashscopeTranscribeInputBody struct {
	FileURLs []string `json:"file_urls"`
}

type dashscopeTranscribeParameters struct {
	LanguageHints            []string `json:"language_hints,omitempty"`
	ChannelID                []int    `json:"channel_id,omitempty"`
	DiarizationEnabled       *bool    `json:"diarization_enabled,omitempty"`
	SpeakerCount             int      `json:"speaker_count,omitempty"`
	DisfluencyRemovalEnabled *bool    `json:"disfluency_removal_enabled,omitempty"`
}

// TranslateTranscribeRequest — 两条路径按入参分流:
//
//	req.Audio != nil (multipart 上传的音频字节) → 同步 qwen3-asr 多模态生成
//	  (base64 内联, 一次 POST 出文字)。handler 的 sync 路径走这里。
//	req.FileURLs 非空 → 异步 paraformer submit (file_url)。handler 的 async
//	  路径走这里 (随后 poll + 二次 fetch)。
func (a *Adaptor) TranslateTranscribeRequest(
	ctx context.Context, req *provider.TranscribeRequest, creds *provider.Credentials,
) (*http.Request, error) {
	if creds == nil || creds.APIKey == "" {
		return nil, fmt.Errorf("dashscope: missing API key")
	}

	// 同步 qwen3-asr 路径: 有音频字节, 无 file_urls。
	if req.Audio != nil && len(req.FileURLs) == 0 {
		return a.translateQwenASR(ctx, req, creds)
	}

	if len(req.FileURLs) == 0 {
		return nil, fmt.Errorf("%w: dashscope ASR 需要 audio bytes (qwen3-asr 同步) 或 file_urls (paraformer 异步)",
			provider.ErrNotImplemented)
	}

	upstream := dashscopeTranscribeRequest{
		Model: req.Model,
		Input: dashscopeTranscribeInputBody{
			FileURLs: req.FileURLs,
		},
	}
	if req.Language != "" {
		upstream.Parameters.LanguageHints = []string{req.Language}
	}

	body, err := json.Marshal(upstream)
	if err != nil {
		return nil, fmt.Errorf("dashscope: marshal transcribe: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+transcribeSubmitPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-DashScope-Async", "enable")
	return httpReq, nil
}

// qwenASRRequest — qwen3-asr-flash 多模态生成 wire shape (同步)。
type qwenASRRequest struct {
	Model string `json:"model"`
	Input struct {
		Messages []qwenASRMessage `json:"messages"`
	} `json:"input"`
}

type qwenASRMessage struct {
	Role    string              `json:"role"`
	Content []map[string]string `json:"content"` // [{"audio":"data:..."}]
}

// translateQwenASR 把音频字节 base64 内联进多模态生成请求。
func (a *Adaptor) translateQwenASR(
	ctx context.Context, req *provider.TranscribeRequest, creds *provider.Credentials,
) (*http.Request, error) {
	raw, err := io.ReadAll(req.Audio)
	if err != nil {
		return nil, fmt.Errorf("dashscope: read audio: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("dashscope: empty audio")
	}
	dataURI := "data:" + audioMimeFromName(req.AudioFilename) + ";base64," +
		base64.StdEncoding.EncodeToString(raw)

	var body qwenASRRequest
	body.Model = req.Model
	body.Input.Messages = []qwenASRMessage{
		{Role: "user", Content: []map[string]string{{"audio": dataURI}}},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("dashscope: marshal qwen-asr: %w", err)
	}

	base := defaultBaseURL
	if creds.BaseURL != "" {
		base = provider.NormalizeBaseURL(creds.BaseURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+multimodalGenPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+creds.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

// ParseTranscribeResponse — 解 qwen3-asr 多模态同步响应:
//
//	output.choices[0].message.content[*].text  → 转写文本 (多段 join)
//	output.choices[0].message.annotations[].language → 语言 (auto-detect)
func (a *Adaptor) ParseTranscribeResponse(body []byte) (*provider.TranscribeResponse, error) {
	var raw struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content []struct {
						Text string `json:"text,omitempty"`
					} `json:"content"`
					Annotations []struct {
						Language string `json:"language,omitempty"`
						Type     string `json:"type,omitempty"`
					} `json:"annotations,omitempty"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
		Code    string `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("dashscope: parse qwen-asr response: %w", err)
	}
	if raw.Code != "" {
		return nil, fmt.Errorf("dashscope qwen-asr %s: %s", raw.Code, raw.Message)
	}
	if len(raw.Output.Choices) == 0 {
		return nil, fmt.Errorf("dashscope qwen-asr: empty choices")
	}
	msg := raw.Output.Choices[0].Message
	var texts []string
	for _, c := range msg.Content {
		if c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	out := &provider.TranscribeResponse{Text: strings.Join(texts, "")}
	for _, an := range msg.Annotations {
		if an.Language != "" {
			out.Language = an.Language
			break
		}
	}
	return out, nil
}

// audioMimeFromName — 由文件名后缀推 MIME (data URI 用)。默认 audio/wav。
func audioMimeFromName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".mp4":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".webm":
		return "audio/webm"
	case ".amr":
		return "audio/amr"
	default:
		return "audio/wav"
	}
}

// ParseTranscribeSubmit — submit 响应解 task_id.
func (a *Adaptor) ParseTranscribeSubmit(body []byte) (string, error) {
	var or struct {
		Output struct {
			TaskID string `json:"task_id"`
		} `json:"output"`
		Code      string `json:"code,omitempty"`
		Message   string `json:"message,omitempty"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return "", fmt.Errorf("dashscope: parse transcribe submit: %w", err)
	}
	if or.Code != "" {
		return "", fmt.Errorf("dashscope transcribe submit: %s — %s", or.Code, or.Message)
	}
	if or.Output.TaskID == "" {
		return "", fmt.Errorf("dashscope transcribe submit: missing task_id (request_id=%s)", or.RequestID)
	}
	return or.Output.TaskID, nil
}

// BuildTranscribePollRequest — 跟 image/video 共用 poll path.
func (a *Adaptor) BuildTranscribePollRequest(
	ctx context.Context, taskID string, creds *provider.Credentials,
) (*http.Request, error) {
	return a.BuildPollRequest(ctx, taskID, creds)
}

// ParseTranscribePollResponse —
// PENDING/QUEUING/RUNNING → "running".
// SUCCEEDED → "succeeded" + resultURL=results[0].transcription_url
//
//	(paraformer 一次只接一个 file_url, 取第一个 result; 多 file 场景目前
//	 不暴露给客户端, M6.5 后续 patch 再扩).
//
// FAILED → "failed" + err.
func (a *Adaptor) ParseTranscribePollResponse(body []byte) (string, *provider.TranscribeResponse, string, error) {
	var or struct {
		Output struct {
			TaskStatus string `json:"task_status"`
			Code       string `json:"code,omitempty"`
			Message    string `json:"message,omitempty"`
			Results    []struct {
				FileURL          string `json:"file_url,omitempty"`
				TranscriptionURL string `json:"transcription_url,omitempty"`
				SubtaskStatus    string `json:"subtask_status,omitempty"`
				Code             string `json:"code,omitempty"`
				Message          string `json:"message,omitempty"`
			} `json:"results"`
		} `json:"output"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &or); err != nil {
		return "", nil, "", fmt.Errorf("dashscope: parse transcribe poll: %w", err)
	}

	switch strings.ToUpper(or.Output.TaskStatus) {
	case "PENDING", "QUEUING", "RUNNING", "UNKNOWN", "":
		return "running", nil, "", nil

	case "SUCCEEDED":
		if len(or.Output.Results) == 0 {
			return "failed", nil, "",
				fmt.Errorf("dashscope transcribe: SUCCEEDED but no results (request_id=%s)", or.RequestID)
		}
		first := or.Output.Results[0]
		// subtask 失败 (e.g. 单个 file 解析失败) 当 task 失败处理.
		if strings.EqualFold(first.SubtaskStatus, "FAILED") {
			code := first.Code
			if code == "" {
				code = "SUBTASK_FAILED"
			}
			return "failed", nil, "",
				fmt.Errorf("dashscope transcribe subtask %s: %s", code, first.Message)
		}
		if first.TranscriptionURL == "" {
			return "failed", nil, "",
				fmt.Errorf("dashscope transcribe: SUCCEEDED but missing transcription_url")
		}
		// dashscope 走 redirect 形态 — 让 caller 二次 fetch.
		return "succeeded", nil, first.TranscriptionURL, nil

	case "FAILED":
		code := or.Output.Code
		if code == "" {
			code = "UPSTREAM_FAILED"
		}
		return "failed", nil, "",
			fmt.Errorf("dashscope transcribe %s: %s", code, or.Output.Message)

	default:
		return "running", nil, "", nil
	}
}

// ParseTranscriptionResult — 解二次 fetch 拿到的 transcription JSON.
//
// dashscope shape:
//
//	{
//	  "file_url": "...",
//	  "properties": {...},
//	  "transcripts": [{
//	    "channel_id": 0,
//	    "content_duration_in_milliseconds": 30000,
//	    "text": "完整的转写文本",
//	    "sentences": [
//	      {"begin_time": 0, "end_time": 1500, "text": "...", "words": [...]}
//	    ]
//	  }]
//	}
//
// 转 canonical TranscribeResponse:
//
//	text     = transcripts[0].text (多 channel 时 join, 但 paraformer 单
//	           channel mono 是绝大多数, 这里 join 简单粗暴)
//	duration = transcripts[0].content_duration_in_milliseconds / 1000
//	segments = sentences (begin/end_time 是 ms, OpenAI canonical 用秒, 转换)
func (a *Adaptor) ParseTranscriptionResult(body []byte) (*provider.TranscribeResponse, error) {
	var raw struct {
		FileURL    string `json:"file_url,omitempty"`
		Properties struct {
			AudioFormat                    string `json:"audio_format,omitempty"`
			ChannelsAmount                 int    `json:"channels_amount,omitempty"`
			OriginalSamplingRate           int    `json:"original_sampling_rate,omitempty"`
			OriginalDurationInMilliseconds int64  `json:"original_duration_in_milliseconds,omitempty"`
		} `json:"properties,omitempty"`
		Transcripts []struct {
			ChannelID                     int    `json:"channel_id"`
			ContentDurationInMilliseconds int64  `json:"content_duration_in_milliseconds"`
			Text                          string `json:"text"`
			Sentences                     []struct {
				BeginTime int64  `json:"begin_time"` // ms
				EndTime   int64  `json:"end_time"`   // ms
				Text      string `json:"text"`
				Words     []struct {
					BeginTime int64  `json:"begin_time"`
					EndTime   int64  `json:"end_time"`
					Text      string `json:"text"`
					// punctuation 字段忽略
				} `json:"words,omitempty"`
			} `json:"sentences,omitempty"`
		} `json:"transcripts"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("dashscope: parse transcription JSON: %w", err)
	}
	if len(raw.Transcripts) == 0 {
		return nil, fmt.Errorf("dashscope: transcription has no transcripts")
	}

	// 多 channel join + 取最大 duration.
	out := &provider.TranscribeResponse{}
	var texts []string
	var maxDurationMs int64
	for _, t := range raw.Transcripts {
		if t.Text != "" {
			texts = append(texts, t.Text)
		}
		if t.ContentDurationInMilliseconds > maxDurationMs {
			maxDurationMs = t.ContentDurationInMilliseconds
		}
		for i, s := range t.Sentences {
			out.Segments = append(out.Segments, provider.TranscribedSeg{
				ID:    i,
				Start: float64(s.BeginTime) / 1000.0,
				End:   float64(s.EndTime) / 1000.0,
				Text:  s.Text,
			})
			for _, w := range s.Words {
				out.Words = append(out.Words, provider.TranscribedWord{
					Word:  w.Text,
					Start: float64(w.BeginTime) / 1000.0,
					End:   float64(w.EndTime) / 1000.0,
				})
			}
		}
	}
	out.Text = strings.Join(texts, " ")
	if maxDurationMs > 0 {
		out.Duration = float64(maxDurationMs) / 1000.0
	} else if raw.Properties.OriginalDurationInMilliseconds > 0 {
		out.Duration = float64(raw.Properties.OriginalDurationInMilliseconds) / 1000.0
	}
	return out, nil
}

// 编译期断言 — adaptor 同时满足 TranscribeAdaptor + AsyncTranscribeAdaptor.
var (
	_ provider.TranscribeAdaptor      = (*Adaptor)(nil)
	_ provider.AsyncTranscribeAdaptor = (*Adaptor)(nil)
)
