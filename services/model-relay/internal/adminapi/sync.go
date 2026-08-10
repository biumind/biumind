// sync.go — POST /v1/admin/models/sync-upstream.
//
// Pulls models.json + vendors.json from MODEL_RELAY_SYNC_UPSTREAM and
// reconciles into the model_relay catalogue.
//
// Design choices:
//
//  1. Source = basellm.github.io/llm-metadata (re-using the data set
//     new-api ships; it covers 5000+ models). The format is fixed:
//     {data: [{model_name, vendor_name, description, tags, status,
//     price_per_m_input, price_per_m_output, ...}]}.
//
//  2. ETag cached in-memory on the Server. First call hits upstream
//     with no If-None-Match; subsequent calls send the saved ETag and
//     a 304 short-circuits to "no changes". A process restart re-pulls
//     once. Persisting the ETag to DB would require a tiny config
//     table — not worth it given the typical sync cadence (manual,
//     once a week at most).
//
//  3. Field whitelist: a sync only updates display_name, family,
//     context_window, capabilities, icon. status / min_plan / sort_order
//     / manual_override stay admin-controlled. New rows land
//     status=disabled so admins must explicitly enable.
//
//  4. manual_override=true models are skipped. The admin's hand
//     supersedes upstream.
//
//  5. dedupe by model_name. The upstream lists the same model under
//     many vendors (302.AI, AIHubMix, ...); BiuMind's models table is
//     keyed by code (== model_name) so we keep the first occurrence
//     and skip duplicates.
//
//  6. Pricing: derived only for *new* models, in USD (upstream's unit).
//     Existing models keep their admin-set pricing.

package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// upstreamModel mirrors the basellm.github.io/llm-metadata model JSON.
// We only decode the fields we actually use.
type upstreamModel struct {
	ModelName           string  `json:"model_name"`
	VendorName          string  `json:"vendor_name"`
	Description         string  `json:"description"`
	Icon                string  `json:"icon"`
	Tags                string  `json:"tags"`
	Status              int     `json:"status"`
	NameRule            int     `json:"name_rule"`
	PricePerMInput      float64 `json:"price_per_m_input"`
	PricePerMOutput     float64 `json:"price_per_m_output"`
	PricePerMCacheRead  float64 `json:"price_per_m_cache_read"`
	PricePerMCacheWrite float64 `json:"price_per_m_cache_write"`
	// Mode 上游不一定提供; 我们在 inferMode 里兜底.
	Mode string `json:"mode"`
}

type upstreamEnvelope struct {
	Data []upstreamModel `json:"data"`
}

// upstreamCache is the per-process ETag cache. Protected by a mutex
// because admin tabs may fire concurrent sync requests.
type upstreamCache struct {
	mu       sync.Mutex
	etag     string
	body     []byte
	syncedAt time.Time
}

// Server.upstream is lazy-initialised on first sync.
var serverUpstream sync.Map // *Server -> *upstreamCache

func (s *Server) upstreamCacheFor() *upstreamCache {
	if v, ok := serverUpstream.Load(s); ok {
		return v.(*upstreamCache)
	}
	c := &upstreamCache{}
	actual, _ := serverUpstream.LoadOrStore(s, c)
	return actual.(*upstreamCache)
}

const defaultUpstreamBase = "https://basellm.github.io/llm-metadata"

func (s *Server) syncUpstreamURL() string {
	if s.SyncUpstreamURL != "" {
		return s.SyncUpstreamURL
	}
	return defaultUpstreamBase
}

func (s *Server) syncHTTPClient() *http.Client {
	if s.SyncHTTPClient != nil {
		return s.SyncHTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// syncResponse is the shape returned by sync-upstream.
type syncResponse struct {
	Added       int       `json:"added"`
	Updated     int       `json:"updated"`
	Skipped     int       `json:"skipped"`
	Total       int       `json:"total"`
	NotModified bool      `json:"not_modified"`
	SyncedAt    time.Time `json:"synced_at"`
	ETag        string    `json:"etag,omitempty"`
}

// POST /v1/admin/models/sync-upstream
func (s *Server) handleSyncUpstream(w http.ResponseWriter, r *http.Request) {
	cache := s.upstreamCacheFor()
	cache.mu.Lock()
	prevETag := cache.etag
	cache.mu.Unlock()

	url := s.syncUpstreamURL() + "/api/newapi/models.json"
	req, _ := http.NewRequestWithContext(r.Context(), "GET", url, nil)
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}

	resp, err := s.syncHTTPClient().Do(req)
	if err != nil {
		metrics.RecordModelRelaySync("upstream_error")
		writeError(w, http.StatusBadGateway, "upstream_unreachable", err.Error())
		return
	}
	defer resp.Body.Close()

	// 304 → return cached counts (zero-delta sync).
	if resp.StatusCode == http.StatusNotModified {
		metrics.RecordModelRelaySync("not_modified")
		writeJSON(w, http.StatusOK, syncResponse{
			NotModified: true,
			SyncedAt:    cache.syncedAt,
			ETag:        prevETag,
		})
		return
	}
	if resp.StatusCode != http.StatusOK {
		metrics.RecordModelRelaySync("upstream_error")
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		writeError(w, http.StatusBadGateway, "upstream_status",
			fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, body))
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		metrics.RecordModelRelaySync("upstream_error")
		writeError(w, http.StatusBadGateway, "upstream_read", err.Error())
		return
	}
	var env upstreamEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		metrics.RecordModelRelaySync("parse_error")
		writeError(w, http.StatusBadGateway, "upstream_parse", err.Error())
		return
	}

	// Persist ETag for next call.
	newETag := resp.Header.Get("ETag")
	cache.mu.Lock()
	cache.etag = newETag
	cache.body = body
	cache.syncedAt = time.Now().UTC()
	cache.mu.Unlock()

	res, err := s.reconcile(r.Context(), env.Data, actorIDFromCtx(r))
	if err != nil {
		metrics.RecordModelRelaySync("reconcile_error")
		writeError(w, http.StatusInternalServerError, "reconcile_failed", err.Error())
		return
	}
	metrics.RecordModelRelaySync("ok")
	res.ETag = newETag
	res.SyncedAt = cache.syncedAt
	writeJSON(w, http.StatusOK, res)
}

// reconcile walks the upstream list once, dedupes by model_name, and
// upserts into model_relay.models. New rows land status='disabled'
// + bound to default group + with a USD pricing row. Existing rows
// follow the field whitelist + manual_override skip rule.
func (s *Server) reconcile(
	ctx context.Context, items []upstreamModel, actor *uuid.UUID,
) (syncResponse, error) {
	res := syncResponse{Total: len(items)}

	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ModelName == "" || item.Status != 1 {
			res.Skipped++
			continue
		}
		if _, dup := seen[item.ModelName]; dup {
			res.Skipped++
			continue
		}
		seen[item.ModelName] = struct{}{}

		caps, contextWindow := parseTags(item.Tags)
		family := familyFromName(item.ModelName)
		display := item.ModelName

		existing, err := s.Store.Models.GetByCode(ctx, item.ModelName)
		if err != nil && !errors.Is(err, registry.ErrNotFound) {
			return res, fmt.Errorf("lookup %s: %w", item.ModelName, err)
		}

		if existing != nil {
			if existing.ManualOverride {
				res.Skipped++
				continue
			}
			updated := *existing
			updated.DisplayName = display
			updated.Family = family
			updated.Capabilities = caps
			if contextWindow > 0 {
				updated.ContextWindow = contextWindow
			}
			// Stamp upstream_ref so a later sync can audit provenance.
			updated.UpstreamRef = &registry.UpstreamRef{
				VendorName: item.VendorName,
				NameRule:   item.NameRule,
				SourceETag: "",
				SyncedAt:   time.Now().UTC(),
			}

			_, err := s.Store.Models.Update(ctx, updated.ID, registry.ModelInput{
				Code:            updated.Code,
				DisplayName:     updated.DisplayName,
				Family:          updated.Family,
				ContextWindow:   updated.ContextWindow,
				MaxOutput:       updated.MaxOutput,
				Capabilities:    updated.Capabilities,
				MinPlan:         updated.MinPlan,
				Status:          updated.Status,
				SortOrder:       updated.SortOrder,
				RoutingStrategy: updated.RoutingStrategy,
				ManualOverride:  updated.ManualOverride,
				// 透传 existing.Mode — 历史脏数据由 backfill migration 修正,
				// sync 不主动覆盖以免冲掉管理员手工设置.
				Mode: updated.Mode,
				// Preserve the admin-set default-chat flag; sync must
				// never clear it (zero value would).
				IsDefaultChat: updated.IsDefaultChat,
			})
			if err != nil {
				return res, fmt.Errorf("update %s: %w", item.ModelName, err)
			}
			if err := s.Store.Models.SetUpstreamRef(ctx, updated.ID, updated.UpstreamRef); err != nil {
				return res, fmt.Errorf("set upstream_ref %s: %w", item.ModelName, err)
			}
			res.Updated++
			continue
		}

		// Insert new model — disabled by default; admin enables manually.
		// Mode: 上游 JSON 若提供且合法则用之, 否则按 model_name 启发式推断.
		mode := item.Mode
		if !registry.IsValidMode(mode) {
			mode = inferMode(item.ModelName, item.VendorName, item.Tags)
		}
		created, err := s.Store.Models.Insert(ctx, registry.ModelInput{
			Code:            item.ModelName,
			DisplayName:     display,
			Family:          family,
			ContextWindow:   contextWindow,
			Capabilities:    caps,
			MinPlan:         registry.PlanFree,
			Status:          registry.StatusDisabled,
			SortOrder:       0,
			RoutingStrategy: registry.StrategyWeighted,
			ManualOverride:  false,
			Mode:            mode,
		})
		if err != nil {
			return res, fmt.Errorf("insert %s: %w", item.ModelName, err)
		}
		// Bind to default group so the visibility filter accepts it
		// once the admin enables it.
		if err := s.Store.Groups.SetModelBindings(ctx, created.ID,
			[]uuid.UUID{registry.DefaultGroupID}); err != nil {
			return res, fmt.Errorf("bind default group %s: %w", item.ModelName, err)
		}
		// Set initial pricing in USD if upstream provided non-zero rates.
		if item.PricePerMInput > 0 || item.PricePerMOutput > 0 {
			_, err := s.Store.Pricing.Set(ctx, registry.PricingInput{
				ModelID:           created.ID,
				Currency:          registry.CurrencyUSD,
				InputPerMTok:      item.PricePerMInput,
				OutputPerMTok:     item.PricePerMOutput,
				CacheWritePerMTok: item.PricePerMCacheWrite,
				CacheReadPerMTok:  item.PricePerMCacheRead,
				CreatedBy:         actor,
			})
			if err != nil {
				return res, fmt.Errorf("seed pricing %s: %w", item.ModelName, err)
			}
		}
		// Stamp upstream_ref last (after the row exists).
		_ = s.Store.Models.SetUpstreamRef(ctx, created.ID, &registry.UpstreamRef{
			VendorName: item.VendorName,
			NameRule:   item.NameRule,
			SyncedAt:   time.Now().UTC(),
		})
		res.Added++
	}
	return res, nil
}

// inferMode 推断模型 mode. 三层防御:
//
//  1. LiteLLM 字典 lookup — LiteLLM 维护的 model_prices_and_context_window.json
//     社区滚动更新, 收录了 OpenAI/Azure/Bedrock/Vertex/Cohere 等主流厂商的
//     非-chat 模型 mode 标注 (~448 条). 命中即权威. 见 litellm_mode_index.go.
//  2. 关键字启发式 — LiteLLM 没收录的国产/小众模型走这里. 比如阿里 cosyvoice
//     (TTS) / paraformer (ASR) / qwen-image / 字节 seedance / hidream / recraft.
//  3. 默认 ModeChat — 全部不命中时兜底 (schema CHECK + DEFAULT 也是 chat).
//
// 关键约束: 上游 basellm.github.io/models.json 不提供 mode 字段, 所以
// 同步链路必须自己分类.
//
// 启发式关键字 (按优先级, 命中即返回):
//   - embedding: bge / *-embed* / embedding / jina-embed / voyage / text-embedding / e5- / gte-
//   - image_generation: dall-e / sd-* / stable-diffusion / flux / midjourney / imagen / kolors
//   - video_generation: sora / runway / pika / kling / veo / cogvideo / hunyuan-video
//   - audio_speech (TTS): tts- / -tts / elevenlabs / *speech*
//   - audio_transcription (ASR): whisper / *transcribe* / *asr*
//
// tags 字段也参与匹配 (basellm 部分模型 tags 里写了 "Embedding"/"TTS").
func inferMode(modelName, vendor, tags string) string {
	// 第 1 层: LiteLLM 字典 — 命中即权威, 跳过启发式
	if mode := lookupLiteLLMMode(modelName); mode != "" {
		return mode
	}

	// 第 2 层: 关键字启发式
	// 全部小写化 + 合成单字符串便于一次性 substring 匹配.
	hay := strings.ToLower(modelName + " | " + vendor + " | " + tags)

	// 0) rerank — 必须先于 embedding 启发式 (bge-reranker-v2-m3 同时含
	//    "bge-" 和 "rerank", 顺序错了会被归到 embedding).
	//    覆盖: bge-reranker / cohere-rerank / qwen3-reranker / jina-reranker /
	//          voyage-rerank / mxbai-rerank / nvidia-rerank / gte-rerank
	if strings.Contains(hay, "rerank") ||
		strings.Contains(hay, "reranker") {
		return registry.ModeRerank
	}

	// 1) embedding (bge-m3 / text-embedding-3 / jina-embeddings / voyage-3 / e5- / gte- / m3e- / qwen3-embedding)
	if strings.Contains(hay, "embed") ||
		strings.Contains(hay, "bge-") ||
		strings.HasPrefix(strings.ToLower(modelName), "bge") ||
		strings.Contains(hay, "/bge") ||
		strings.Contains(hay, "voyage-") ||
		strings.HasPrefix(strings.ToLower(modelName), "e5-") ||
		strings.HasPrefix(strings.ToLower(modelName), "gte-") ||
		strings.HasPrefix(strings.ToLower(modelName), "m3e-") ||
		strings.Contains(hay, "/m3e-") ||
		strings.Contains(hay, "nomic-embed") {
		return registry.ModeEmbedding
	}

	// 2) image generation
	//   - 国外: dall-e / stable-diffusion / sd-* / flux / midjourney / imagen / kolors / ideogram / hidream / recraft
	//   - 国产: qwen-image / hunyuan-image / cogview / seedream
	if strings.Contains(hay, "dall-e") ||
		strings.Contains(hay, "stable-diffusion") ||
		strings.Contains(hay, "stable diffusion") ||
		strings.HasPrefix(strings.ToLower(modelName), "sd-") ||
		strings.Contains(hay, "/sd-") ||
		strings.Contains(hay, "flux") ||
		strings.Contains(hay, "midjourney") ||
		strings.Contains(hay, "imagen") ||
		strings.Contains(hay, "kolors") ||
		strings.Contains(hay, "ideogram") ||
		strings.Contains(hay, "hidream") ||
		strings.Contains(hay, "recraft") ||
		strings.Contains(hay, "qwen-image") ||
		strings.Contains(hay, "qwen/qwen-image") ||
		strings.Contains(hay, "hunyuan-image") ||
		strings.Contains(hay, "cogview") ||
		strings.Contains(hay, "seedream") {
		return registry.ModeImageGeneration
	}

	// 3) video generation
	//   - 国外: sora / runway / pika / veo / mochi
	//   - 国产: kling / cogvideo / hunyuan-video / wan- (wanx) / seedance / hailuo / vidu / minimax-video
	if strings.Contains(hay, "sora") ||
		strings.Contains(hay, "runway") ||
		strings.Contains(hay, "pika") ||
		strings.Contains(hay, "kling") ||
		strings.HasPrefix(strings.ToLower(modelName), "veo") ||
		strings.Contains(hay, "cogvideo") ||
		strings.Contains(hay, "hunyuan-video") ||
		strings.Contains(hay, "wan-") ||
		strings.Contains(hay, "wanx") ||
		strings.Contains(hay, "seedance") ||
		strings.Contains(hay, "hailuo") ||
		strings.Contains(hay, "/vidu") ||
		strings.HasPrefix(strings.ToLower(modelName), "vidu") ||
		strings.Contains(hay, "minimax-video") ||
		strings.Contains(hay, "mochi") {
		return registry.ModeVideoGeneration
	}

	// 4) ASR — whisper / transcribe / asr / paraformer / sensevoice / funasr / seed-asr
	//   (放在 TTS 之前, 避免 "tts-whisper" 这种命名冲突)
	if strings.Contains(hay, "whisper") ||
		strings.Contains(hay, "transcrib") ||
		strings.Contains(hay, "-asr") ||
		strings.HasPrefix(strings.ToLower(modelName), "asr-") ||
		strings.Contains(hay, "paraformer") ||
		strings.Contains(hay, "sensevoice") ||
		strings.Contains(hay, "funasr") ||
		strings.Contains(hay, "seed-asr") {
		return registry.ModeAudioTranscription
	}

	// 5) TTS — tts / speech / elevenlabs / cosyvoice / chattts / fish-speech / fish-audio /
	//          spark-tts / indextts / melotts / styletts / voicecraft / maskgct
	if strings.Contains(hay, "tts") ||
		strings.Contains(hay, "elevenlabs") ||
		strings.Contains(hay, "cosyvoice") ||
		strings.Contains(hay, "chattts") ||
		strings.Contains(hay, "fish-speech") ||
		strings.Contains(hay, "fish-audio") ||
		strings.Contains(hay, "spark-tts") ||
		strings.Contains(hay, "indextts") ||
		strings.Contains(hay, "melotts") ||
		strings.Contains(hay, "styletts") ||
		strings.Contains(hay, "voicecraft") ||
		strings.Contains(hay, "maskgct") ||
		(strings.Contains(hay, "speech") && !strings.Contains(hay, "speech-to-text")) {
		return registry.ModeAudioSpeech
	}

	// 6) 默认: 对话 LLM
	return registry.ModeChat
}

// parseTags maps the comma-separated upstream tag string to capabilities
// + context window. Examples:
//
//	"Tools,Files,Vision,200K"    → {tools, vision, cache?:false}, 200_000
//	"Reasoning,Tools,Vision,128K" → {tools, vision, thinking}, 128_000
//	"1M"                          → 1_000_000
//
// Unknown tags are silently ignored — upstream may add new ones.
func parseTags(tags string) (registry.Capabilities, int) {
	var caps registry.Capabilities
	var ctxWin int
	for _, t := range strings.Split(tags, ",") {
		t = strings.TrimSpace(t)
		switch strings.ToLower(t) {
		case "tools":
			caps.Tools = true
		case "vision":
			caps.Vision = true
		case "reasoning", "thinking":
			caps.Thinking = true
		case "audio":
			caps.Audio = true
		case "json", "jsonmode":
			caps.JSONMode = true
		case "cache":
			caps.Cache = true
		default:
			if n := parseContextSize(t); n > 0 {
				ctxWin = n
			}
		}
	}
	return caps, ctxWin
}

// parseContextSize handles "128K" / "1M" / "32k" → tokens.
func parseContextSize(s string) int {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	mul := 1
	switch s[len(s)-1] {
	case 'K':
		mul = 1_000
		s = s[:len(s)-1]
	case 'M':
		mul = 1_000_000
		s = s[:len(s)-1]
	default:
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n * mul
}

// familyFromName guesses a family bucket from the model name. Coarse
// heuristic — admin can edit afterward. Saves the operator from typing
// "claude" / "gpt" 5000 times.
func familyFromName(name string) string {
	low := strings.ToLower(name)
	switch {
	case strings.HasPrefix(low, "claude"):
		return "claude"
	case strings.HasPrefix(low, "gpt"), strings.HasPrefix(low, "chatgpt"),
		strings.HasPrefix(low, "o1"), strings.HasPrefix(low, "o3"),
		strings.HasPrefix(low, "o4"):
		return "openai"
	case strings.HasPrefix(low, "gemini"):
		return "gemini"
	case strings.HasPrefix(low, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(low, "qwen"):
		return "qwen"
	case strings.HasPrefix(low, "llama"):
		return "llama"
	case strings.HasPrefix(low, "kimi"), strings.HasPrefix(low, "moonshot"):
		return "kimi"
	case strings.HasPrefix(low, "glm"):
		return "zhipu"
	case strings.HasPrefix(low, "grok"):
		return "grok"
	case strings.HasPrefix(low, "mistral"):
		return "mistral"
	default:
		return "other"
	}
}
