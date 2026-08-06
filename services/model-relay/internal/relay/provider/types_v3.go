// v0.3 全模态网关 — 各 modality 的请求/响应/流式类型.
//
// 设计原则:
//   1. **OpenAI 兼容优先** — 所有 Request/Response 字段命名贴齐 OpenAI
//      规范, 让客户端用 OpenAI SDK 直接打到 BiuMind. native 协议字段在
//      adaptor 层处理, 不外泄.
//   2. **跨 modality 公共字段抽 BaseRequest** — Model + Stream + Headers,
//      避免每个 Request struct 重复.
//   3. **流式用 channel** — Speech/Transcribe/Chat 流式输出统一 <-chan
//      Frame; 非流式直接返回完整 Response.
//   4. **三阶段计费用 Credits + Ratios** — Credits 是积分 int64, Ratios
//      是 estimate→submit→complete 之间传递的乘数 map (见 new-api
//      TaskAdaptor 设计).
//
// 见 docs/BiuMind-Multimodal-Gateway-Design.md §3 / §5.

package provider

import (
	"context"
	"io"
	"time"
)

// Credits 是计费积分 (int64), 与 identity.holds 的单位一致.
// 1 元 = 100,000 毫分; 1 积分 = 1000 毫分 (按 BiuMind-Billing-Redesign §3).
type Credits = int64

// ─── 1. Embed (向量) ──────────────────────────────────────────────

type EmbedRequest struct {
	Model string
	// Input 支持 string 或 []string. 用 any 让 adaptor 各自处理 (OpenAI
	// 接受两种形态). adaptor 必须能处理两种.
	Input any
	// EncodingFormat — "float" (默认) 或 "base64".
	EncodingFormat string
	// Dimensions — 可选, 仅 text-embedding-3-* 等支持降维的模型用.
	Dimensions int
	// User — OpenAI 推荐传, 用于 abuse 检测; 透传到上游.
	User string
}

type EmbedResponse struct {
	Object string      `json:"object"` // "list"
	Data   []EmbedData `json:"data"`
	Model  string      `json:"model"`
	Usage  EmbedUsage  `json:"usage"`
}

type EmbedData struct {
	Object    string    `json:"object"` // "embedding"
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type EmbedUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ─── 2. Speech (TTS) ──────────────────────────────────────────────

type SpeechRequest struct {
	Model string
	Input string // 待合成文本
	// Voice — voice 名字, OpenAI 是 alloy/echo/fable/...; cosyvoice 是
	// longxiaochun_v2 等. adaptor 的 MapOpenAIParams 负责跨 provider 映射.
	Voice string
	// ResponseFormat — "mp3" / "opus" / "aac" / "flac" / "wav" / "pcm".
	// 默认 "mp3" (OpenAI 默认).
	ResponseFormat string
	// Speed — 朗读速度倍率, 不是所有 provider 支持; 不支持时 silently drop.
	Speed float64
	// SampleRate — 部分 provider 支持自定义采样率 (cosyvoice 22050/44100).
	SampleRate int
}

// AudioFrame 是流式 TTS 的一帧音频字节 + 元数据.
// 流式按 ResponseFormat 切片; mp3 一帧 ~ 24KB / 1s.
//
// 错误传播: adaptor 解析 SSE 遇到非协议响应(如 voice 不合法上游返 JSON
// 错误体)时, 推一个 AudioFrame{Err: ...} 终结流. handler 在 WriteHeader
// 之前看到 Err 应该把它转成 502; 在已写过 Data 之后看到 Err 只能 break
// 流(HTTP 不给改 status). 这避免了 "200 OK + 0 bytes" 的伪成功.
type AudioFrame struct {
	Data       []byte    // 原始音频字节 (mp3/opus/...)
	MimeType   string    // "audio/mpeg" / "audio/opus" / "audio/wav"
	DurationMs int       // 该帧音频时长 (ms), 用于流式累加计费
	Final      bool      // true 表示这是最后一帧
	ReceivedAt time.Time // 收到时间, 用于 latency metrics

	// Characters — 计费维度 (M5). dashscope SSE 在最终帧透传 usage.characters,
	// adaptor 解析时填进去给 handler 用. 0 = adaptor 没拿到 / 上游没透传,
	// handler 兜底用 input 字符数估.
	Characters int

	// Err — 流式上游错误透传. 非 nil 时这帧不携带 Data; handler 应:
	//   - 若还没 WriteHeader(200): writeJSONErr(502, ...)
	//   - 若已开始流: break 循环 + 记日志
	Err error
}

// ─── 3. Transcribe (ASR) ─────────────────────────────────────────

type TranscribeRequest struct {
	Model string
	// Audio — 音频字节流; multipart/form-data 已经在 handler 解析过.
	// 同步 adaptor (Whisper) 必填; 异步 adaptor (paraformer) 用 FileURLs.
	Audio io.Reader
	// AudioFilename — multipart 原始文件名 (用于 ext 推断).
	AudioFilename string
	// FileURLs — M6.5: 异步 adaptor (paraformer-v2 / sensevoice) 用.
	// 必须公网可访问 https URL (上游 GPU worker 自己拉取). 客户端可直接
	// 传 OSS / CDN URL, 或先把音频上传到我们 brain /v1/files 拿 presigned
	// URL 再传.
	FileURLs []string
	// Language — ISO-639-1 (zh/en/...) 不传 = auto detect.
	Language string
	// Prompt — 引导 LLM 转写风格 (whisper 支持).
	Prompt string
	// ResponseFormat — "json" / "text" / "srt" / "verbose_json" / "vtt".
	ResponseFormat string
	// Temperature — 0.0 = 确定性最高 (whisper 默认 0).
	Temperature float64
}

type TranscribeResponse struct {
	Text     string            `json:"text"`
	Language string            `json:"language,omitempty"`
	Duration float64           `json:"duration,omitempty"` // 秒
	Words    []TranscribedWord `json:"words,omitempty"`
	Segments []TranscribedSeg  `json:"segments,omitempty"`
}

type TranscribedWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type TranscribedSeg struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// ─── 4. Image (生成) ─────────────────────────────────────────────

type ImageRequest struct {
	Model  string
	Prompt string
	// NegativePrompt — 反向提示词 (dashscope wanx / SD 支持; dall-e 忽略)。
	// 段 3.6: aigc 文生图经 model-relay egress 时需透传, 否则丢参数。
	NegativePrompt string
	// Seed — 随机种子 (>0 固定可复现; 0 随机)。dashscope 支持。
	Seed int
	// N — 生成图片张数 (OpenAI dall-e-3 仅支持 1).
	N int
	// Size — "1024x1024" / "1024x1792" / "1792x1024" / 各 provider 自定义.
	// 显式给定时优先;为空时各 adaptor 按 AspectRatio+Resolution 查自己的尺寸表。
	Size string
	// AspectRatio / Resolution — 段3.6: 让 size 映射收敛进各 provider adaptor
	// (dashscope 与 volcengine 的尺寸表不同),worker 不再预算 size。
	AspectRatio string
	Resolution  string
	// ReferenceImageURLs — 参考图 (volcengine Seedream image:[] / dashscope ref_img)。
	ReferenceImageURLs []string
	// Quality — "standard" / "hd" (dall-e-3); 部分 provider 用 "high"/"medium".
	Quality string
	// Style — "vivid" / "natural" (dall-e-3).
	Style string
	// ResponseFormat — "url" / "b64_json".
	ResponseFormat string
	User           string
}

type ImageResponse struct {
	Created int64       `json:"created"` // unix timestamp
	Data    []ImageData `json:"data"`
}

type ImageData struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ─── 4b. Video (生成, M4) ────────────────────────────────────────
//
// 视频生成跟图像同 async 模式 (submit → task_id → poll → URL), 但参数维度
// 多 (duration / resolution), 输出多一个 cover_image_url. 所以用独立的
// VideoRequest / VideoResponse 类型, 不共用 ImageRequest.

type VideoRequest struct {
	Model  string
	Prompt string
	// NegativePrompt — 反向提示词 (可选).
	NegativePrompt string

	// 输入形态 (互斥):
	//   纯文 → 都不填
	//   首帧 → FirstFrameURL
	//   首尾帧 → FirstFrameURL + LastFrameURL
	//   参考图 → ReferenceImageURLs (可与 First/Last 共存视模型而定)
	FirstFrameURL      string
	LastFrameURL       string
	ReferenceImageURLs []string

	// Size — "1280*720" / "1920*1080" / 各模型支持的预设, 同 ImageRequest.
	Size string
	// DurationSeconds — 视频时长. wanx 通常 5/10s, kling 5-10s.
	DurationSeconds int
	// Seed — 复现用, 可选.
	Seed int
	// AspectRatio — "16:9" / "9:16" / "1:1", 部分模型用此代替 Size.
	AspectRatio string
	// Resolution — "720p" / "1080p", 跟 AspectRatio 配套.
	Resolution string

	User string
}

type VideoResponse struct {
	Created int64       `json:"created"` // unix timestamp
	Data    []VideoData `json:"data"`
}

type VideoData struct {
	// URL — 生成视频的 URL (mp4 等). adaptor 应规范化跨厂商字段名.
	URL string `json:"url"`
	// CoverImageURL — 视频封面缩略图 URL, 可选.
	CoverImageURL string `json:"cover_image_url,omitempty"`
	// DurationMs — 实际生成视频的时长 (ms), 可选 (不是所有 provider 透传).
	DurationMs int `json:"duration_ms,omitempty"`
	// RevisedPrompt — 上游 LLM 改写后的 prompt (dashscope prompt_extend 等).
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ─── 5. Rerank (RAG 排序) ────────────────────────────────────────

type RerankRequest struct {
	Model string
	Query string
	// Documents — 待排序的候选段落.
	Documents []string
	// TopN — 只返回前 N 个 (按相关性 desc); 不传 = 返回全部.
	TopN int
	// ReturnDocuments — true 时返回原始文本; false 时只返 index.
	ReturnDocuments bool
}

type RerankResponse struct {
	ID      string         `json:"id"`
	Results []RerankResult `json:"results"`
	Meta    RerankMeta     `json:"meta"`
}

type RerankResult struct {
	Index          int             `json:"index"`
	RelevanceScore float64         `json:"relevance_score"`
	Document       *RerankDocument `json:"document,omitempty"` // ReturnDocuments=true 时填
}

type RerankDocument struct {
	Text string `json:"text"`
}

type RerankMeta struct {
	BilledUnits RerankBilledUnits `json:"billed_units"`
}

type RerankBilledUnits struct {
	SearchUnits int `json:"search_units"`
}

// ─── 6. Task (异步长任务: image/video/digital_human/hotparse) ────

// TaskRequest 是 /v1/jobs 接到的统一请求形态. 各 adaptor 用 mode 决定
// 怎么解析 Params.
type TaskRequest struct {
	Model          string
	Mode           string // image_generation / video_generation / digital_human / hotparse
	Prompt         string
	NegativePrompt string
	// Params — 自由 map, 含 size/duration/voice/n 等. adaptor 在
	// EstimateBilling 时按 mode 解析.
	Params map[string]any
	// ParentSHA — 上游可能要的"上一帧"或"上一张图"引用 (img2img / video frame).
	ParentSHA string
}

type TaskStatus struct {
	Status         string // pending / queued / running / succeeded / failed / cancelled
	Progress       int    // 0..100
	ExternalTaskID string
	ErrorCode      string
	ErrorMessage   string
	CreatedAt      time.Time
	CompletedAt    *time.Time
	// RawResponseFromUpstream — adaptor 想保留的上游原始响应 (供
	// AdjustBillingOnComplete / ParseFinalOutput 二次解析).
	RawResponseFromUpstream []byte
}

type TaskOutput struct {
	Idx        int    `json:"idx"`
	Kind       string `json:"kind"` // image / video / audio / cover
	StorageURL string `json:"storage_url"`
	Sha256     string `json:"sha256"`
	MimeType   string `json:"mime_type,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
}

// ─── 7. WebSocket session (流式实时双向) ─────────────────────────

// WebSocketSession 是上游 WS 连接的抽象, 让 adaptor 不依赖具体 ws 库.
// model-relay 的 WS handler 拿到 session 后做 read/write/close 转发.
type WebSocketSession interface {
	// Send 把文本消息发给上游 (例如 cosyvoice native run-task).
	Send(ctx context.Context, msg []byte) error
	// Receive 从上游读一帧 — 返回字节流 + 是否二进制 (audio = true; control = false).
	Receive(ctx context.Context) (data []byte, binary bool, err error)
	// Close 关闭上游 WS, normal close 用 1000 code.
	Close(code int, reason string) error
}
