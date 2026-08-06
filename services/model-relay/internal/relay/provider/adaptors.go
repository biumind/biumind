// v0.3 全模态网关 — 按 modality 拆分的 Adaptor interface 集合.
//
// 设计取舍:
//   - 老 Adaptor interface (provider.go:237) 是 chat-only 的, 它在
//     200+ 个调用方里. 这次**保留不动**, 让现有 chat 路径零回归.
//   - 新增的 modality interface 都嵌入 BaseAdaptor (Name + Capabilities),
//     provider 选实现哪些 = 选支持哪些 modality.
//   - ChatAdaptor 故意嵌入老 Adaptor + 加 Capabilities, 这样
//     openai/anthropic 仅多实现一个 method 就同时满足新老接口.
//   - ParamMapper 是横切关注点 (LiteLLM map_openai_params 风格), 任何
//     adaptor 都可选实现 — Router 在调用前看 type assertion 决定是否调.
//
// 路由层用法 (M0.3 ModeRouter):
//
//   adaptor, _ := registry.Get("dashscope")
//   switch model.Mode {
//   case ModeAudioSpeech:
//     speechA, ok := adaptor.(SpeechAdaptor)
//     if !ok { return 415 unsupported }
//     ...
//   }
//
// 见 docs/BiuMind-Multimodal-Gateway-Design.md §3.

package provider

import (
	"context"
	"net/http"
)

// BaseAdaptor 是所有 modality adaptor 的公共底座.
// Name 是 provider 唯一标识 ("openai" / "anthropic" / "dashscope" / ...).
// Capabilities 声明该 adaptor 实现了哪些 modality, 路由层用来快速过滤.
//
// Capabilities 返回字符串建议用 registry.Mode* 常量值 (chat / embedding /
// audio_speech / audio_transcription / image_generation / video_generation /
// rerank / responses), 但不强制 — 调用方应忽略未知 capability 而非报错.
type BaseAdaptor interface {
	Name() string
	Capabilities() []string
}

// ChatAdaptor — chat completions 路径 (含流式 + tool use).
// 嵌入老 Adaptor 让现有实现自动满足.
type ChatAdaptor interface {
	Adaptor // 老接口: Name / TranslateRequest / ParseResponse / StreamAdapter
	// Capabilities 让 ChatAdaptor 也满足 BaseAdaptor.
	Capabilities() []string
}

// EmbedAdaptor — /v1/embeddings 路径.
// 同步语义, 无流式.
type EmbedAdaptor interface {
	BaseAdaptor
	TranslateEmbedRequest(ctx context.Context, req *EmbedRequest, creds *Credentials) (*http.Request, error)
	ParseEmbedResponse(body []byte) (*EmbedResponse, error)
}

// SpeechAdaptor — TTS, /v1/audio/speech 路径.
// HTTP 路径流式返回 AudioFrame; WS 路径走 OpenSpeechWebSocket (M5).
type SpeechAdaptor interface {
	BaseAdaptor
	TranslateSpeechRequest(ctx context.Context, req *SpeechRequest, creds *Credentials) (*http.Request, error)
	// StreamAudioFrames 解析上游 chunked transfer 的音频字节, 转 AudioFrame
	// channel. body 是 http.Response.Body (caller 负责 close).
	StreamAudioFrames(ctx context.Context, body interface{ Read([]byte) (int, error) }) (<-chan AudioFrame, error)
	// OpenSpeechWebSocket — M5 实现; M1 默认返回 ErrNotImplemented.
	OpenSpeechWebSocket(ctx context.Context, req *SpeechRequest, creds *Credentials) (WebSocketSession, error)
}

// TranscribeAdaptor — ASR 同步路径, /v1/audio/transcriptions multipart upload
// (Whisper / Groq / SiliconFlow / 自部署 faster-whisper).
type TranscribeAdaptor interface {
	BaseAdaptor
	TranslateTranscribeRequest(ctx context.Context, req *TranscribeRequest, creds *Credentials) (*http.Request, error)
	ParseTranscribeResponse(body []byte) (*TranscribeResponse, error)
}

// AsyncTranscribeAdaptor — ASR 异步 file_url 模式 (paraformer-v2 / sensevoice).
//
// dashscope 是 3 步:
//   1. submit (file_url) → task_id
//   2. poll task → SUCCEEDED + transcription_url (二次)
//   3. fetch transcription_url → 真正的 ASR JSON
//
// 接口设计为 caller (handler) 持 httpClient 做 HTTP IO; adaptor 只负责
// translate/parse. ParseTranscribePollResponse 同时提供两种成功形态:
//   - inline: result 不空 (上游一次出全部结果)
//   - redirect: resultURL 不空 (上游 dashscope 风格, caller 再 fetch)
//
// 路径选择: 嵌入 TranscribeAdaptor 让 adaptor 也可同时支持 sync (用 Audio
// io.Reader) 和 async (用 FileURLs). dashscope 仅实现 async, sync method
// 应返 ErrNotImplemented.
type AsyncTranscribeAdaptor interface {
	TranscribeAdaptor

	// ParseTranscribeSubmit — submit 响应解 task_id.
	ParseTranscribeSubmit(body []byte) (taskID string, err error)

	// BuildTranscribePollRequest — GET task 状态.
	BuildTranscribePollRequest(ctx context.Context, taskID string, creds *Credentials) (*http.Request, error)

	// ParseTranscribePollResponse —
	//   status="running"   → result/resultURL 都空
	//   status="succeeded" → result 不空 (inline) 或 resultURL 不空 (redirect)
	//   status="failed"    → err 不空
	ParseTranscribePollResponse(body []byte) (status string, result *TranscribeResponse, resultURL string, err error)

	// ParseTranscriptionResult — 二次 fetch transcription_url 拿到的 JSON
	// 解析成 canonical TranscribeResponse. 只 dashscope 等 redirect 形态
	// 上游需要; 一步出结果的 provider 可返 ErrNotImplemented.
	ParseTranscriptionResult(body []byte) (*TranscribeResponse, error)
}

// ImageAdaptor — /v1/images/generations 同步路径 (DALL-E / 自部署 SD 等).
// 上游收 1 次 HTTP 直接返图片 URL. 长任务用 AsyncImageAdaptor 包成
// "客户端看着 sync, 内部 submit+poll" 的 facade.
type ImageAdaptor interface {
	BaseAdaptor
	TranslateImageRequest(ctx context.Context, req *ImageRequest, creds *Credentials) (*http.Request, error)
	ParseImageResponse(body []byte) (*ImageResponse, error)
}

// AsyncImageAdaptor — 上游 submit + poll 模式的图像生成 (wanx / 部分
// midjourney 中转 / kolors 等). 在 ImageAdaptor 之上加 3 个方法让 handler
// 走 submit → 拿 task_id → poll 直到 SUCCEEDED 的循环.
//
// handler 类型断言决定路径:
//
//   if asyncA, ok := adaptor.(provider.AsyncImageAdaptor); ok {
//       // submit + poll loop
//   } else {
//       // sync 单次 HTTP
//   }
//
// 同一 adaptor 也可同时实现 ImageAdaptor (sync) 和 AsyncImageAdaptor —
// 后者优先 (provider.dashscope 走 async).
type AsyncImageAdaptor interface {
	ImageAdaptor

	// ParseImageSubmit 解 submit 响应, 拿到上游分配的 task_id.
	// 失败时返 err (例如上游 quota 用尽 / 参数非法).
	ParseImageSubmit(body []byte) (taskID string, err error)

	// BuildPollRequest 构造查任务状态的 GET 请求.
	BuildPollRequest(ctx context.Context, taskID string, creds *Credentials) (*http.Request, error)

	// ParsePollResponse 解 poll 响应:
	//   status="running" → result=nil, err=nil  (caller 继续 poll)
	//   status="succeeded" → result 不空, err=nil  (caller 返回客户端)
	//   status="failed"   → result=nil, err 不空 (caller 502)
	// 实现层应规范化 PENDING/RUNNING/QUEUED → "running" 等.
	ParsePollResponse(body []byte) (status string, result *ImageResponse, err error)
}

// VideoAdaptor — /v1/videos/generations 同步路径 (假想未来出现的 sync
// 视频模型). 现实中视频几乎全部 async, 所以 dashscope.Adaptor 实现
// AsyncVideoAdaptor 接口.
type VideoAdaptor interface {
	BaseAdaptor
	TranslateVideoRequest(ctx context.Context, req *VideoRequest, creds *Credentials) (*http.Request, error)
	ParseVideoResponse(body []byte) (*VideoResponse, error)
}

// AsyncVideoAdaptor — submit + poll 模式 (wanx-video / kling-video / mochi
// 等). 跟 AsyncImageAdaptor 平行结构, M4 起用.
//
// handler 类型断言:
//   if asyncV, ok := adaptor.(AsyncVideoAdaptor); ok {
//       // submit → poll loop
//   } else if syncV, ok := adaptor.(VideoAdaptor); ok {
//       // 单次 HTTP
//   }
type AsyncVideoAdaptor interface {
	VideoAdaptor

	// ParseVideoSubmit 解 submit 响应拿 task_id.
	ParseVideoSubmit(body []byte) (taskID string, err error)

	// BuildVideoPollRequest 构造查任务状态的 GET 请求. 命名跟
	// BuildPollRequest (image) 区分, 避免同 adaptor 同时实现两路时方法名
	// 冲突 (Go interface satisfaction 看签名匹配).
	BuildVideoPollRequest(ctx context.Context, taskID string, creds *Credentials) (*http.Request, error)

	// ParseVideoPollResponse 解 poll 响应:
	//   status="running" → result=nil
	//   status="succeeded" → result 不空 (含 video_url + cover_image_url)
	//   status="failed" → result=nil, err 不空
	ParseVideoPollResponse(body []byte) (status string, result *VideoResponse, err error)
}

// RerankAdaptor — /v1/rerank 路径 (Cohere/Jina/bge-reranker).
type RerankAdaptor interface {
	BaseAdaptor
	TranslateRerankRequest(ctx context.Context, req *RerankRequest, creds *Credentials) (*http.Request, error)
	ParseRerankResponse(body []byte) (*RerankResponse, error)
}

// TaskAdaptor — 异步长任务 (image/video/digital_human/hotparse) 通过 /v1/jobs.
// 三阶段计费: estimate → submit → complete (借鉴 new-api TaskAdaptor 设计).
//
// 阶段 1: EstimateBilling — 提交前按用户参数估算 (例: 5s 视频 × cost_per_video_second)
// 阶段 2: BuildSubmitRequest → 上游 ack → AdjustBillingOnSubmit
//         (上游可能调整接受参数, 例: 用户请求 5s 实际接受 3s)
// 阶段 3: PollTaskStatus → 完成后 AdjustBillingOnComplete + ParseFinalOutput
//         (按上游回报的实际产出秒数/帧数最终结算 delta)
type TaskAdaptor interface {
	BaseAdaptor
	EstimateBilling(ctx context.Context, req *TaskRequest) (estimate Credits, ratios map[string]float64, err error)
	BuildSubmitRequest(ctx context.Context, req *TaskRequest, creds *Credentials) (*http.Request, error)
	AdjustBillingOnSubmit(submitResp []byte) (adjustedRatios map[string]float64)
	PollTaskStatus(ctx context.Context, externalTaskID string, creds *Credentials) (*TaskStatus, error)
	AdjustBillingOnComplete(status *TaskStatus) (actualCredits Credits)
	ParseFinalOutput(status *TaskStatus) ([]TaskOutput, error)
}

// ParamMapper 是横切接口, 任何 adaptor 都可选实现.
//
// SupportedOpenAIParams 让上层能告知客户端"该 model 实际支持哪些
// OpenAI 参数" (例: cosyvoice 不支持 speed); MapOpenAIParams 把 OpenAI
// 兼容参数翻译成 native 参数并 silently drop 不支持的 (LiteLLM 默认行为).
//
// 设计参考 LiteLLM 的 BaseXxxConfig (per-modality transformation) 接口.
type ParamMapper interface {
	SupportedOpenAIParams(model string) []string
	MapOpenAIParams(model string, openaiParams map[string]any) (nativeParams map[string]any, err error)
}
