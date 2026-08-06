// Package health provides one-shot probe of a credential or channel
// by sending a minimal "hello" request to the upstream provider. Two
// callers:
//
//   1. Admin "test credential" / "test channel" buttons — synchronous,
//      shows latency + error in the UI.
//   2. Supervisor cron (M2.7) — periodic probe of auto_disabled channels
//      to recover them when the upstream is healthy again.
//
// The probe runs through the same provider.Adaptor used in the real
// request path, so a passing probe is strong evidence the upstream
// integration is correct (not just "TCP reachable"). It pays one upstream
// LLM call per probe — keep cron interval generous (5-10 min default).

package health

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// Sentinel error categories. ProbeResult.ErrorCode carries one of these
// strings so admin UI / cron logic can branch without parsing the
// human-readable Error message. Stable values — used in metrics labels.
const (
	CodeOK           = ""
	CodeTimeout      = "timeout"
	CodeUnauthorized = "unauthorized" // 401 / 403
	CodeRateLimited  = "rate_limited" // 429
	CodeServer       = "server_error" // 5xx
	CodeNetwork      = "network"      // TCP / TLS / DNS
	CodeDecrypt      = "decrypt"      // envelope unwrap failed
	CodeUnsupported  = "unsupported"  // no adaptor registered for provider
	CodeBadResponse  = "bad_response" // 2xx but body unparseable
)

// ProbeResult is the outcome of a single hello probe.
type ProbeResult struct {
	OK         bool   `json:"ok"`
	LatencyMs  int    `json:"latency_ms"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
	Tokens     int    `json:"tokens,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}

// Config wires the probe with its dependencies. Adaptors must be the
// same registry the relay path uses — a probe that doesn't exercise
// the production code path is worse than no probe.
type Config struct {
	Store    *registry.Store
	Vault    *registry.CredentialVault
	Adaptors *provider.Registry

	// HTTPClient defaults to a 10s-timeout client. Override for tests
	// (httptest.Server doesn't need long timeouts).
	HTTPClient *http.Client

	// Timeout is the per-probe deadline. Defaults to 10s. Network
	// failures slower than this are reported as CodeTimeout.
	Timeout time.Duration

	// DefaultTestModel is used by RunCredential when the caller doesn't
	// pass an explicit model (admin "test credential" path). Per-protocol
	// fallback: openai_compat → "gpt-4o-mini", anthropic → "claude-haiku".
	DefaultTestModel map[provider.Adaptor]string

	Logger *slog.Logger
}

func (c *Config) defaults() {
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: c.Timeout}
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Probe executes hello probes. Construct once, reuse across requests.
type Probe struct {
	cfg Config
}

func New(cfg Config) *Probe {
	if cfg.Store == nil {
		panic("health.New: Store required")
	}
	if cfg.Vault == nil {
		panic("health.New: Vault required")
	}
	if cfg.Adaptors == nil {
		panic("health.New: Adaptors required")
	}
	cfg.defaults()
	return &Probe{cfg: cfg}
}

// RunChannel probes a specific channel. The caller usually has the ID
// already (admin button) or the channel struct in hand (cron sweep).
// Status doesn't matter — supervisor cron probes auto_disabled channels
// to recover them.
func (p *Probe) RunChannel(ctx context.Context, channelID uuid.UUID) *ProbeResult {
	ch, err := p.cfg.Store.Channels.Get(ctx, channelID)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("get channel: %v", err),
		}
	}
	model, err := p.cfg.Store.Models.Get(ctx, ch.ModelID)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("get model: %v", err),
		}
	}
	return p.runWithChannel(ctx, ch, model.Code, ch.UpstreamModel, model.Mode)
}

// RunCredential probes a credential WITHOUT a specific channel. Used
// by admin "test credential" — the credential may not yet be wired to
// any channel. testModel can be empty: the probe falls back to a per-
// protocol default. Caller may pass a base URL override (rare).
func (p *Probe) RunCredential(ctx context.Context, credentialID uuid.UUID, testModel string) *ProbeResult {
	plaintext, cred, err := p.cfg.Vault.RevealForProbe(ctx, credentialID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return &ProbeResult{
				OK: false, ErrorCode: CodeNetwork,
				Error: "credential not found",
			}
		}
		return &ProbeResult{
			OK: false, ErrorCode: CodeDecrypt,
			Error: fmt.Sprintf("decrypt: %v", err),
		}
	}
	defer wipe(plaintext)

	prov, err := p.cfg.Store.Providers.Get(ctx, cred.ProviderID)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("get provider: %v", err),
		}
	}
	adaptor := p.lookupAdaptor(prov)
	if adaptor == nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeUnsupported,
			Error: fmt.Sprintf("no adaptor for protocol %s", prov.Protocol),
		}
	}
	model := testModel
	if model == "" {
		model = p.defaultTestModelFor(adaptor)
	}
	return p.runHTTP(ctx, adaptor, plaintext, cred.BaseURL, cred.HeaderOverride, model, model)
}

// runWithChannel decrypts the channel's credential then runs the probe.
// modelMode controls endpoint dispatch: "chat"/"" → chat completions,
// "embedding" → /v1/embeddings, 其它 mode 短期返回 CodeUnsupported
// (P5 follow-up: image / video / tts / asr probe).
func (p *Probe) runWithChannel(
	ctx context.Context, ch *registry.Channel, displayModel, upstreamModel, modelMode string,
) *ProbeResult {
	plaintext, cred, err := p.cfg.Vault.RevealForProbe(ctx, ch.CredentialID)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeDecrypt,
			Error: fmt.Sprintf("decrypt credential: %v", err),
		}
	}
	defer wipe(plaintext)

	prov, err := p.cfg.Store.Providers.Get(ctx, cred.ProviderID)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("get provider: %v", err),
		}
	}

	switch modelMode {
	case "", registry.ModeChat:
		adaptor := p.lookupAdaptor(prov)
		if adaptor == nil {
			return &ProbeResult{
				OK: false, ErrorCode: CodeUnsupported,
				Error: fmt.Sprintf("no adaptor for protocol %s", prov.Protocol),
			}
		}
		return p.runHTTP(ctx, adaptor, plaintext, cred.BaseURL, cred.HeaderOverride,
			displayModel, upstreamModel)

	case registry.ModeEmbedding:
		base, ok := p.lookupModalityAdaptor(prov)
		if !ok {
			return notFoundAdaptor(prov)
		}
		embedA, ok := base.(provider.EmbedAdaptor)
		if !ok {
			return notImplemented(base, "EmbedAdaptor")
		}
		return p.runEmbeddingHTTP(ctx, embedA, plaintext, cred.BaseURL, cred.HeaderOverride,
			displayModel, upstreamModel)

	case registry.ModeRerank:
		base, ok := p.lookupModalityAdaptor(prov)
		if !ok {
			return notFoundAdaptor(prov)
		}
		rerankA, ok := base.(provider.RerankAdaptor)
		if !ok {
			return notImplemented(base, "RerankAdaptor")
		}
		return p.runRerankHTTP(ctx, rerankA, plaintext, cred.BaseURL, cred.HeaderOverride,
			displayModel, upstreamModel)

	case registry.ModeAudioSpeech:
		base, ok := p.lookupModalityAdaptor(prov)
		if !ok {
			return notFoundAdaptor(prov)
		}
		speechA, ok := base.(provider.SpeechAdaptor)
		if !ok {
			return notImplemented(base, "SpeechAdaptor")
		}
		return p.runSpeechHTTP(ctx, speechA, plaintext, cred.BaseURL, cred.HeaderOverride,
			displayModel, upstreamModel)

	case registry.ModeImageGeneration:
		base, ok := p.lookupModalityAdaptor(prov)
		if !ok {
			return notFoundAdaptor(prov)
		}
		imageA, ok := base.(provider.ImageAdaptor)
		if !ok {
			return notImplemented(base, "ImageAdaptor")
		}
		return p.runImageHTTP(ctx, imageA, plaintext, cred.BaseURL, cred.HeaderOverride,
			displayModel, upstreamModel)

	case registry.ModeVideoGeneration:
		base, ok := p.lookupModalityAdaptor(prov)
		if !ok {
			return notFoundAdaptor(prov)
		}
		videoA, ok := base.(provider.VideoAdaptor)
		if !ok {
			return notImplemented(base, "VideoAdaptor")
		}
		return p.runVideoHTTP(ctx, videoA, plaintext, cred.BaseURL, cred.HeaderOverride,
			displayModel, upstreamModel)

	case registry.ModeAudioTranscription:
		base, ok := p.lookupModalityAdaptor(prov)
		if !ok {
			return notFoundAdaptor(prov)
		}
		transcribeA, ok := base.(provider.TranscribeAdaptor)
		if !ok {
			return notImplemented(base, "TranscribeAdaptor")
		}
		return p.runTranscribeHTTP(ctx, transcribeA, plaintext, cred.BaseURL, cred.HeaderOverride,
			displayModel, upstreamModel)

	default:
		return &ProbeResult{
			OK: false, ErrorCode: CodeUnsupported,
			Error: fmt.Sprintf("admin probe unsupported for mode=%s "+
				"(implemented: chat/embedding/rerank/audio_speech/audio_transcription/image/video)",
				modelMode),
		}
	}
}

// notFoundAdaptor / notImplemented — 把重复的 ProbeResult 错误返回浓缩成
// helper, switch 的 modality 分支看着干净.
func notFoundAdaptor(prov *registry.Provider) *ProbeResult {
	return &ProbeResult{
		OK: false, ErrorCode: CodeUnsupported,
		Error: fmt.Sprintf("no adaptor registered for protocol=%s code=%s",
			prov.Protocol, prov.Code),
	}
}

func notImplemented(base provider.BaseAdaptor, ifaceName string) *ProbeResult {
	return &ProbeResult{
		OK: false, ErrorCode: CodeUnsupported,
		Error: fmt.Sprintf("adaptor %s does not implement %s", base.Name(), ifaceName),
	}
}

// runHTTP is the low-level probe: build "hello" request, dispatch,
// classify response.
func (p *Probe) runHTTP(
	ctx context.Context,
	adaptor provider.Adaptor,
	plaintext []byte,
	baseURL string,
	header map[string]string,
	displayModel, upstreamModel string,
) *ProbeResult {
	maxTok := 16
	creds := &provider.Credentials{
		APIKey:  string(plaintext),
		BaseURL: baseURL,
	}
	req := &provider.Request{
		Model:     upstreamModel,
		Messages:  []provider.Message{{Role: "user", Content: provider.JSONString("hello")}},
		MaxTokens: maxTok,
		Stream:    false,
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	httpReq, err := adaptor.TranslateRequest(probeCtx, req, creds)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("translate: %v", err),
		}
	}
	for k, v := range header {
		httpReq.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	// Ceil to whole ms — sub-ms loopback responses (httptest) would
	// otherwise show as 0, which the supervisor / admin UI would
	// mistake for "no measurement".
	elapsed := time.Since(start)
	latency := int((elapsed + time.Millisecond - 1) / time.Millisecond)
	if latency < 1 && err == nil {
		latency = 1
	}
	if err != nil {
		code := CodeNetwork
		if errors.Is(err, context.DeadlineExceeded) {
			code = CodeTimeout
		}
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			ErrorCode: code, Error: err.Error(),
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode != http.StatusOK {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  classifyStatus(resp.StatusCode),
			Error:      truncate(string(body), 200),
		}
	}

	parsed, err := adaptor.ParseResponse(body)
	if err != nil {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      fmt.Sprintf("parse: %v", err),
		}
	}
	tokens := parsed.Usage.PromptTokens + parsed.Usage.CompletionTokens
	p.cfg.Logger.Debug("model_relay probe ok",
		"model", displayModel, "upstream_model", upstreamModel,
		"latency_ms", latency, "tokens", tokens)
	return &ProbeResult{
		OK: true, LatencyMs: latency,
		StatusCode: resp.StatusCode,
		Tokens:     tokens,
	}
}

// runEmbeddingHTTP — embedding probe. 走 EmbedAdaptor 翻译 + ParseEmbedResponse
// 解析, 跟 relay 路径用同一接口 (零漂移). 之前是 inline 手写 OpenAI shape,
// M2.3 重构跟 chat / speech 风格对齐.
//
// 验证: response.data[0].embedding 不空 → 链路通. token 用量透传到 Tokens
// 字段供 admin UI 显示.
func (p *Probe) runEmbeddingHTTP(
	ctx context.Context,
	embedA provider.EmbedAdaptor,
	plaintext []byte,
	baseURL string,
	header map[string]string,
	displayModel, upstreamModel string,
) *ProbeResult {
	creds := buildProbeCreds(plaintext, baseURL, header)
	req := &provider.EmbedRequest{
		Model: upstreamModel,
		Input: "hello",
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	httpReq, err := embedA.TranslateEmbedRequest(probeCtx, req, creds)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("translate: %v", err),
		}
	}
	for k, v := range header {
		httpReq.Header.Set(k, v)
	}

	resp, body, latency, errResult := p.doProbe(httpReq)
	if errResult != nil {
		return errResult
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  classifyStatus(resp.StatusCode),
			Error:      truncate(string(body), 200),
		}
	}

	parsed, err := embedA.ParseEmbedResponse(body)
	if err != nil {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      fmt.Sprintf("parse: %v", err),
		}
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      "empty embedding in response",
		}
	}
	p.cfg.Logger.Debug("model_relay embedding probe ok",
		"model", displayModel, "upstream_model", upstreamModel,
		"latency_ms", latency, "dim", len(parsed.Data[0].Embedding),
		"prompt_tokens", parsed.Usage.PromptTokens)
	return &ProbeResult{
		OK: true, LatencyMs: latency,
		StatusCode: resp.StatusCode,
		Tokens:     parsed.Usage.PromptTokens,
	}
}

// runRerankHTTP — rerank probe. 1 query + 2 docs, 拿到 results 即认为
// 链路通. search_units 透传到 Tokens 字段 (跟 embedding 一致).
func (p *Probe) runRerankHTTP(
	ctx context.Context,
	rerankA provider.RerankAdaptor,
	plaintext []byte,
	baseURL string,
	header map[string]string,
	displayModel, upstreamModel string,
) *ProbeResult {
	creds := buildProbeCreds(plaintext, baseURL, header)
	req := &provider.RerankRequest{
		Model:     upstreamModel,
		Query:     "hello",
		Documents: []string{"world", "foo"},
		TopN:      1,
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	httpReq, err := rerankA.TranslateRerankRequest(probeCtx, req, creds)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("translate: %v", err),
		}
	}
	for k, v := range header {
		httpReq.Header.Set(k, v)
	}

	resp, body, latency, errResult := p.doProbe(httpReq)
	if errResult != nil {
		return errResult
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  classifyStatus(resp.StatusCode),
			Error:      truncate(string(body), 200),
		}
	}

	parsed, err := rerankA.ParseRerankResponse(body)
	if err != nil {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      fmt.Sprintf("parse: %v", err),
		}
	}
	if len(parsed.Results) == 0 {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      "no results in rerank response",
		}
	}
	p.cfg.Logger.Debug("model_relay rerank probe ok",
		"model", displayModel, "upstream_model", upstreamModel,
		"latency_ms", latency, "results", len(parsed.Results),
		"search_units", parsed.Meta.BilledUnits.SearchUnits)
	return &ProbeResult{
		OK: true, LatencyMs: latency,
		StatusCode: resp.StatusCode,
		Tokens:     parsed.Meta.BilledUnits.SearchUnits,
	}
}

// runImageHTTP — image generation probe. AsyncImageAdaptor 路径只验
// submit 拿到 task_id (不等出图, 否则 probe 要 10-30s); 同步 ImageAdaptor
// 路径走完整 ParseImageResponse.
//
// 设计意图: probe 是健康度检测, "submit 成功 → 上游 quota / 鉴权 / 模型
// 名都对" 已经覆盖 90% 的故障表面. 真正出图阶段的失败 (内容审核 / GPU
// 排队超时) 是产线运营问题, 不是 channel 健康问题, 不该让 probe 被它阻塞.
func (p *Probe) runImageHTTP(
	ctx context.Context,
	imageA provider.ImageAdaptor,
	plaintext []byte,
	baseURL string,
	header map[string]string,
	displayModel, upstreamModel string,
) *ProbeResult {
	creds := buildProbeCreds(plaintext, baseURL, header)
	req := &provider.ImageRequest{
		Model:  upstreamModel,
		Prompt: "a red apple", // 短 prompt, 命中所有模型的内容审核
		N:      1,
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	httpReq, err := imageA.TranslateImageRequest(probeCtx, req, creds)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("translate: %v", err),
		}
	}
	for k, v := range header {
		httpReq.Header.Set(k, v)
	}

	resp, body, latency, errResult := p.doProbe(httpReq)
	if errResult != nil {
		return errResult
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  classifyStatus(resp.StatusCode),
			Error:      truncate(string(body), 200),
		}
	}

	// async 路径: 解 task_id 即认为通; 不 poll 等出图.
	if asyncA, ok := imageA.(provider.AsyncImageAdaptor); ok {
		taskID, perr := asyncA.ParseImageSubmit(body)
		if perr != nil {
			return &ProbeResult{
				OK: false, LatencyMs: latency,
				StatusCode: resp.StatusCode,
				ErrorCode:  CodeBadResponse,
				Error:      fmt.Sprintf("submit parse: %v", perr),
			}
		}
		p.cfg.Logger.Debug("model_relay image probe ok (async submit)",
			"model", displayModel, "upstream_model", upstreamModel,
			"latency_ms", latency, "task_id", taskID)
		return &ProbeResult{
			OK: true, LatencyMs: latency,
			StatusCode: resp.StatusCode,
		}
	}

	// sync 路径 (DALL-E / 自部署 SD): 直接 ParseImageResponse.
	parsed, err := imageA.ParseImageResponse(body)
	if err != nil {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      fmt.Sprintf("parse: %v", err),
		}
	}
	if len(parsed.Data) == 0 {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      "no images in response",
		}
	}
	p.cfg.Logger.Debug("model_relay image probe ok (sync)",
		"model", displayModel, "upstream_model", upstreamModel,
		"latency_ms", latency, "images", len(parsed.Data))
	return &ProbeResult{
		OK: true, LatencyMs: latency,
		StatusCode: resp.StatusCode,
	}
}

// runVideoHTTP — video generation probe. 跟 image probe 同策略: async
// 路径只验 submit 拿 task_id, 不 poll 等出片 (视频要 1-3min, probe 不能
// 等那么久). sync 路径 ParseVideoResponse 当前没 provider 实装, 兜底.
func (p *Probe) runVideoHTTP(
	ctx context.Context,
	videoA provider.VideoAdaptor,
	plaintext []byte,
	baseURL string,
	header map[string]string,
	displayModel, upstreamModel string,
) *ProbeResult {
	creds := buildProbeCreds(plaintext, baseURL, header)
	req := &provider.VideoRequest{
		Model:           upstreamModel,
		Prompt:          "a red apple", // 短 prompt, 跟 image probe 一致
		DurationSeconds: 5,             // 最短时长省钱
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	httpReq, err := videoA.TranslateVideoRequest(probeCtx, req, creds)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("translate: %v", err),
		}
	}
	for k, v := range header {
		httpReq.Header.Set(k, v)
	}

	resp, body, latency, errResult := p.doProbe(httpReq)
	if errResult != nil {
		return errResult
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  classifyStatus(resp.StatusCode),
			Error:      truncate(string(body), 200),
		}
	}

	if asyncA, ok := videoA.(provider.AsyncVideoAdaptor); ok {
		taskID, perr := asyncA.ParseVideoSubmit(body)
		if perr != nil {
			return &ProbeResult{
				OK: false, LatencyMs: latency,
				StatusCode: resp.StatusCode,
				ErrorCode:  CodeBadResponse,
				Error:      fmt.Sprintf("submit parse: %v", perr),
			}
		}
		p.cfg.Logger.Debug("model_relay video probe ok (async submit)",
			"model", displayModel, "upstream_model", upstreamModel,
			"latency_ms", latency, "task_id", taskID)
		return &ProbeResult{
			OK: true, LatencyMs: latency,
			StatusCode: resp.StatusCode,
		}
	}

	// sync 路径 — 当前无 provider 实装, ParseVideoResponse 通常返
	// ErrNotImplemented. 留这分支让接口 SOLID, 不阻塞 probe.
	parsed, err := videoA.ParseVideoResponse(body)
	if err != nil {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      fmt.Sprintf("parse: %v", err),
		}
	}
	if len(parsed.Data) == 0 {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      "no videos in response",
		}
	}
	p.cfg.Logger.Debug("model_relay video probe ok (sync)",
		"model", displayModel, "upstream_model", upstreamModel,
		"latency_ms", latency, "videos", len(parsed.Data))
	return &ProbeResult{
		OK: true, LatencyMs: latency,
		StatusCode: resp.StatusCode,
	}
}

// runTranscribeHTTP — ASR probe (Whisper / GPT-4o-transcribe). 上传一段
// 最小有效 WAV (44 字节 header + 100ms 静音) 验链路通. Whisper 通常会返回
// 空 text 但 200 — 链路 ok; 鉴权/模型名错才会 4xx.
func (p *Probe) runTranscribeHTTP(
	ctx context.Context,
	transcribeA provider.TranscribeAdaptor,
	plaintext []byte,
	baseURL string,
	header map[string]string,
	displayModel, upstreamModel string,
) *ProbeResult {
	creds := buildProbeCreds(plaintext, baseURL, header)
	// 最小 WAV: 16-bit mono 8kHz, 100ms = 1600 字节静音
	wav := minimalWAV()
	req := &provider.TranscribeRequest{
		Model:         upstreamModel,
		Audio:         bytes.NewReader(wav),
		AudioFilename: "probe.wav",
		Language:      "en",
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	httpReq, err := transcribeA.TranslateTranscribeRequest(probeCtx, req, creds)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("translate: %v", err),
		}
	}
	for k, v := range header {
		httpReq.Header.Set(k, v)
	}

	resp, body, latency, errResult := p.doProbe(httpReq)
	if errResult != nil {
		return errResult
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 部分上游对静音音频会返 4xx "audio too short" — 跟鉴权失败区分:
		// 401/403 直接归 auth/forbidden; 其它 4xx 视为链路通 (上游能解析
		// 我们的请求, 模型名/鉴权都对, 只是音频不合规).
		if resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden {
			return &ProbeResult{
				OK: false, LatencyMs: latency,
				StatusCode: resp.StatusCode,
				ErrorCode:  classifyStatus(resp.StatusCode),
				Error:      truncate(string(body), 200),
			}
		}
		// 4xx (e.g. 400 audio_too_short) → 链路 ok 但音频不行, 当成功透出
		p.cfg.Logger.Debug("model_relay transcribe probe ok (upstream rejects empty audio, link OK)",
			"model", displayModel, "upstream_model", upstreamModel,
			"status", resp.StatusCode, "body", truncate(string(body), 200))
		return &ProbeResult{
			OK: true, LatencyMs: latency,
			StatusCode: resp.StatusCode,
		}
	}

	// 200: parse 一下确认格式对
	parsed, err := transcribeA.ParseTranscribeResponse(body)
	if err != nil {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      fmt.Sprintf("parse: %v", err),
		}
	}
	p.cfg.Logger.Debug("model_relay transcribe probe ok",
		"model", displayModel, "upstream_model", upstreamModel,
		"latency_ms", latency, "text_len", len(parsed.Text))
	return &ProbeResult{
		OK: true, LatencyMs: latency,
		StatusCode: resp.StatusCode,
	}
}

// minimalWAV — 100ms 8kHz 16-bit mono 静音, 44 字节 RIFF header + 1600
// 字节 PCM data. probe 用 — 没真音频也能让上游接受请求.
func minimalWAV() []byte {
	const (
		sampleRate    uint32 = 8000
		bitsPerSample uint16 = 16
		numChannels   uint16 = 1
		samples       uint32 = 800 // 100ms @ 8kHz
	)
	dataSize := samples * uint32(numChannels) * uint32(bitsPerSample) / 8
	totalSize := 36 + dataSize
	byteRate := sampleRate * uint32(numChannels) * uint32(bitsPerSample) / 8
	blockAlign := numChannels * bitsPerSample / 8

	buf := bytes.NewBuffer(nil)
	buf.WriteString("RIFF")
	writeLE32(buf, totalSize)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	writeLE32(buf, 16) // fmt chunk size
	writeLE16(buf, 1)  // audio format = PCM
	writeLE16(buf, numChannels)
	writeLE32(buf, sampleRate)
	writeLE32(buf, byteRate)
	writeLE16(buf, blockAlign)
	writeLE16(buf, bitsPerSample)
	buf.WriteString("data")
	writeLE32(buf, dataSize)
	buf.Write(make([]byte, dataSize)) // 静音
	return buf.Bytes()
}

func writeLE32(buf *bytes.Buffer, v uint32) {
	buf.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

func writeLE16(buf *bytes.Buffer, v uint16) {
	buf.Write([]byte{byte(v), byte(v >> 8)})
}

// buildProbeCreds — 给 modality probe 共用的 Credentials 构造.
func buildProbeCreds(plaintext []byte, baseURL string, header map[string]string) *provider.Credentials {
	creds := &provider.Credentials{
		APIKey:  string(plaintext),
		BaseURL: baseURL,
	}
	if len(header) > 0 {
		creds.Extra = make(map[string]string, len(header))
		for k, v := range header {
			creds.Extra[k] = v
		}
	}
	return creds
}

// doProbe — embedding/rerank/image 共用的 HTTP do + latency 统计 + body
// 读取 + network/timeout 错误归类. 把 5 处重复的 90 行 boilerplate 浓缩.
//
// 返 (resp, body, latency_ms, errResult). errResult != nil 时 resp/body
// 都 nil, caller 直接 return; 否则 caller 负责 resp.Body.Close.
func (p *Probe) doProbe(httpReq *http.Request) (*http.Response, []byte, int, *ProbeResult) {
	start := time.Now()
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	elapsed := time.Since(start)
	latency := int((elapsed + time.Millisecond - 1) / time.Millisecond)
	if latency < 1 && err == nil {
		latency = 1
	}
	if err != nil {
		code := CodeNetwork
		if errors.Is(err, context.DeadlineExceeded) {
			code = CodeTimeout
		}
		return nil, nil, latency, &ProbeResult{
			OK: false, LatencyMs: latency,
			ErrorCode: code, Error: err.Error(),
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp, body, latency, nil
}

// runSpeechHTTP — TTS probe (M1: dashscope cosyvoice). 走 SpeechAdaptor
// 翻译 + 上游执行, 拉一帧音频确认链路通即成功. 不解码 audio 内容.
//
// 失败定义:
//   - 凭证错 → upstream 401/403 → CodeAuth/CodeForbidden
//   - voice 写错 / 模型停服 → 4xx 响应体 → upstream_status
//   - 没收到任何 AudioFrame → CodeBadResponse "no audio frames"
func (p *Probe) runSpeechHTTP(
	ctx context.Context,
	speechA provider.SpeechAdaptor,
	plaintext []byte,
	baseURL string,
	header map[string]string,
	displayModel, upstreamModel string,
) *ProbeResult {
	creds := &provider.Credentials{
		APIKey:  string(plaintext),
		BaseURL: baseURL,
	}
	if len(header) > 0 {
		creds.Extra = make(map[string]string, len(header))
		for k, v := range header {
			creds.Extra[k] = v
		}
	}
	// probe voice — 极短文本 + 默认 voice. cosyvoice 的 voice 必填且系统
	// 音色按版本不同 (longanyang / longxiaochun_v2 等). probe 阶段我们不
	// 知道哪个 voice 跟这个 model 匹配, 用 longanyang (cosyvoice-v3-flash
	// 系统音色) 当默认; 用户绑错 model + voice 是配置问题, probe 失败也
	// 反映了真实状态.
	req := &provider.SpeechRequest{
		Model:          upstreamModel,
		Input:          "hi",
		Voice:          "longanyang",
		ResponseFormat: "mp3",
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	httpReq, err := speechA.TranslateSpeechRequest(probeCtx, req, creds)
	if err != nil {
		return &ProbeResult{
			OK: false, ErrorCode: CodeNetwork,
			Error: fmt.Sprintf("translate: %v", err),
		}
	}

	start := time.Now()
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	elapsed := time.Since(start)
	latency := int((elapsed + time.Millisecond - 1) / time.Millisecond)
	if latency < 1 && err == nil {
		latency = 1
	}
	if err != nil {
		code := CodeNetwork
		if errors.Is(err, context.DeadlineExceeded) {
			code = CodeTimeout
		}
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			ErrorCode: code, Error: err.Error(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  classifyStatus(resp.StatusCode),
			Error:      truncate(string(body), 200),
		}
	}

	frames, err := speechA.StreamAudioFrames(probeCtx, resp.Body)
	if err != nil {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			ErrorCode: CodeBadResponse,
			Error:     fmt.Sprintf("stream open: %v", err),
		}
	}

	var totalBytes int
	var frameCount int
	for f := range frames {
		totalBytes += len(f.Data)
		frameCount++
		if frameCount >= 1 && totalBytes > 0 {
			// 一帧有内容就算链路通; 不等流式 stop, 缩短 probe 总时长.
			// 但要继续 drain channel 直到 close 避免 goroutine 泄漏.
		}
	}
	if totalBytes == 0 {
		return &ProbeResult{
			OK: false, LatencyMs: latency,
			StatusCode: resp.StatusCode,
			ErrorCode:  CodeBadResponse,
			Error:      "no audio frames in response",
		}
	}
	p.cfg.Logger.Debug("model_relay speech probe ok",
		"model", displayModel, "upstream_model", upstreamModel,
		"latency_ms", latency, "frames", frameCount, "bytes", totalBytes)
	return &ProbeResult{
		OK: true, LatencyMs: latency,
		StatusCode: resp.StatusCode,
	}
}

// ─── helpers ──────────────────────────────────────────────────────

// lookupAdaptor — chat probe 路径专用: 拿到的 adaptor 必须满足老 chat
// Adaptor 接口 (TranslateRequest/ParseResponse/StreamAdapter). 用 GetChat
// 强制 type-assert. 不满足 chat 接口的 adaptor (例如 dashscope.Adaptor 只
// 实现 SpeechAdaptor) 在这里返 nil — 上层会返 CodeUnsupported.
//
// 不在这里加 protocol=dashscope fallback 是故意的: dashscope chat 模型
// 必须用 protocol=openai_compat 入库, 走 openai adaptor; protocol=dashscope
// 只用于 audio_speech / image / video 等模态, chat probe 不应命中.
//
// audio_speech / embed / image probe 各自单独 dispatch, 见 runWithChannel.
func (p *Probe) lookupAdaptor(prov *registry.Provider) provider.Adaptor {
	if a, ok := p.cfg.Adaptors.GetChat(prov.Code); ok {
		return a
	}
	if a, ok := p.cfg.Adaptors.GetChat(string(prov.Protocol)); ok {
		return a
	}
	if prov.Protocol == registry.ProtocolOpenAICompat {
		if a, ok := p.cfg.Adaptors.GetChat("openai"); ok {
			return a
		}
	}
	if prov.Protocol == registry.ProtocolAnthropic {
		if a, ok := p.cfg.Adaptors.GetChat("anthropic"); ok {
			return a
		}
	}
	return nil
}

// lookupModalityAdaptor — 非 chat 路径用. 跟 router/mode_router.go
// lookupAdaptor 同样的多重 fallback 链条:
//
//  1. provider.Code 精确匹配 ("openai" / "dashscope" / "dashscope-bailian")
//  2. provider.Protocol 名 ("openai_compat" / "anthropic" / "dashscope")
//  3. protocol=openai_compat → "openai" alias
//  4. protocol=anthropic    → "anthropic" alias
//  5. protocol=dashscope    → "dashscope" alias
//
// 返 BaseAdaptor — caller 按 modality 自己 type-assert 到 EmbedAdaptor /
// RerankAdaptor / SpeechAdaptor / ImageAdaptor / AsyncImageAdaptor.
//
// 与 mode_router 同步: 加新 protocol 时这两处都要改 (有专门的集成测试
// router/mode_router_test.go:TestModeRouter_NoAdaptorForProvider 验链条).
func (p *Probe) lookupModalityAdaptor(prov *registry.Provider) (provider.BaseAdaptor, bool) {
	if a, ok := p.cfg.Adaptors.Get(prov.Code); ok {
		return a, true
	}
	if a, ok := p.cfg.Adaptors.Get(string(prov.Protocol)); ok {
		return a, true
	}
	if prov.Protocol == registry.ProtocolOpenAICompat {
		if a, ok := p.cfg.Adaptors.Get("openai"); ok {
			return a, true
		}
	}
	if prov.Protocol == registry.ProtocolAnthropic {
		if a, ok := p.cfg.Adaptors.Get("anthropic"); ok {
			return a, true
		}
	}
	if prov.Protocol == registry.ProtocolDashScope {
		if a, ok := p.cfg.Adaptors.Get("dashscope"); ok {
			return a, true
		}
	}
	return nil, false
}

func (p *Probe) defaultTestModelFor(a provider.Adaptor) string {
	if m, ok := p.cfg.DefaultTestModel[a]; ok {
		return m
	}
	switch a.Name() {
	case "anthropic":
		return "claude-haiku-4-5"
	case "openai", "openai_compat":
		return "gpt-4o-mini"
	default:
		return "test-model"
	}
}

func classifyStatus(code int) string {
	switch {
	case code == 401, code == 403:
		return CodeUnauthorized
	case code == 429:
		return CodeRateLimited
	case code >= 500:
		return CodeServer
	default:
		return CodeBadResponse
	}
}

// wipe best-effort zeroes a byte slice. We can't stop the GC from
// keeping copies — Go strings and []byte conversions copy on read —
// but this scrubs the only buffer we control.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// stringPrefix is exported for tests that want to verify probe error
// messages. Internal use only.
func stringPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
