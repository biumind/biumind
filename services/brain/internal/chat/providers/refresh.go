// Per-provider /models endpoint fetchers.
//
// Each builtin LLM has a different /models surface:
//
//   * Anthropic: GET /v1/models → {"data":[{"id":...,"display_name":...,"created_at":...}, ...]}
//                                  Requires `x-api-key` + `anthropic-version`.
//   * OpenAI:    GET /v1/models → {"object":"list","data":[{"id":...,"created":...}, ...]}
//                                  `Authorization: Bearer <key>`.
//   * Google:    GET /v1beta/models?key=... → {"models":[{"name":"models/gemini-...","displayName":...}, ...]}
//                                              No bearer; key as query param.
//   * Custom:    GET {base_url}/models, OpenAI shape.
//
// We translate each into a uniform list of ModelInput rows and let
// the caller upsert. Upstream errors bubble up as Go errors with
// trimmed body so the API layer can return BadGateway.

package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const httpTimeout = 15 * time.Second

// fetchUpstreamModels dispatches by provider_id and returns the model
// list ready to upsert. Returns an error when the provider has no
// known fetcher (e.g. official) — caller should fall back to static.
//
// P3: 不再从 *Provider 读明文 key (brain 已不存). 调用方现从 identity
// 取 apiKey + base (见 Server.resolveUpstreamCreds), 显式传入. userID
// 落进 ModelInput.UserID 让 upsert 归属正确用户.
func fetchUpstreamModels(ctx context.Context, providerID, base, apiKey string, userID uuid.UUID) ([]ModelInput, error) {
	if apiKey == "" {
		return nil, errors.New("api key not configured")
	}
	switch providerID {
	case "anthropic":
		if base == "" {
			base = "https://api.anthropic.com"
		}
		return fetchAnthropic(ctx, base, apiKey, userID, providerID)
	case "openai":
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return fetchOpenAI(ctx, base, apiKey, userID, providerID)
	case "google":
		if base == "" {
			base = "https://generativelanguage.googleapis.com/v1beta"
		}
		return fetchGoogle(ctx, base, apiKey, userID, providerID)
	default:
		// Custom provider — assume OpenAI-compatible.
		if base == "" {
			return nil, errors.New("base_url is required for custom providers")
		}
		return fetchOpenAI(ctx, base, apiKey, userID, providerID)
	}
}

// ─── Anthropic ──────────────────────────────────────────

func fetchAnthropic(ctx context.Context, base, key string, userID uuid.UUID, providerID string,
) ([]ModelInput, error) {
	endpoint := strings.TrimRight(base, "/") + "/v1/models?limit=100"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	body, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			ID          string    `json:"id"`
			DisplayName string    `json:"display_name"`
			Type        string    `json:"type"`
			CreatedAt   time.Time `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("anthropic /models: %w", err)
	}
	out := make([]ModelInput, 0, len(parsed.Data))
	for i, m := range parsed.Data {
		display := m.DisplayName
		if display == "" {
			display = m.ID
		}
		var released *time.Time
		if !m.CreatedAt.IsZero() {
			t := m.CreatedAt.UTC()
			released = &t
		}
		ab, ctxWin, pricing := anthropicMeta(m.ID)
		out = append(out, ModelInput{
			UserID:        userID,
			ProviderID:    providerID,
			ModelID:       m.ID,
			DisplayName:   display,
			Type:          ModelTypeChat,
			Abilities:     ab,
			ContextWindow: ctxWin,
			Pricing:       pricing,
			ReleasedAt:    released,
			SortOrder:     i,
			Source:        ModelSourceRemote,
		})
	}
	return out, nil
}

// anthropicMeta 返 anthropic 模型的默认元数据 (capabilities/context/pricing)。
// P6: 删静态 BuiltinCatalog 查表 (global 模型改 client 直读 model-relay),
// 一律返合理默认 (BYOK 上游 /models 拉到的 id 不本地建 catalog)。
// pricing nil — BYOK 用户自付, brain 不掌握上游定价。
func anthropicMeta(id string) (map[string]bool, *int, map[string]any) {
	_ = id
	cw := 200_000
	return map[string]bool{"vision": true, "functions": true}, &cw, nil
}

// ─── OpenAI (and OpenAI-compatible) ─────────────────────

func fetchOpenAI(ctx context.Context, base, key string, userID uuid.UUID, providerID string,
) ([]ModelInput, error) {
	// OpenAI 兼容 base 通常以 /v1 结尾(api.openai.com/v1)。用户填代理时
	// 可能只填到域名根(如 https://new-api.example.com/,无 /v1)→ /models
	// 打到 web 首页返 HTML → parse 失败 → fallback catalog。base 不含 /v1
	// 时自动补,让 endpoint 正确(https://new-api.example.com/v1/models)。
	ep := strings.TrimRight(base, "/")
	if !strings.HasSuffix(ep, "/v1") {
		ep += "/v1"
	}
	endpoint := ep + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	body, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Created int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openai /models: %w", err)
	}
	out := make([]ModelInput, 0, len(parsed.Data))
	for i, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		var released *time.Time
		if m.Created > 0 {
			t := time.Unix(m.Created, 0).UTC()
			released = &t
		}
		ab, cw, pricing := openaiMeta(providerID, m.ID)
		out = append(out, ModelInput{
			UserID:        userID,
			ProviderID:    providerID,
			ModelID:       m.ID,
			DisplayName:   m.ID,
			Type:          guessType(m.ID),
			Abilities:     ab,
			ContextWindow: cw,
			Pricing:       pricing,
			ReleasedAt:    released,
			SortOrder:     i,
			Source:        ModelSourceRemote,
		})
	}
	return out, nil
}

// openaiMeta 返 OpenAI / OpenAI-compat 模型默认元数据。P6: 删静态
// BuiltinCatalog 查表, 一律返空 abilities (BYOK 上游 id 不本地建 catalog)。
func openaiMeta(providerID, id string) (map[string]bool, *int, map[string]any) {
	_ = providerID
	_ = id
	return map[string]bool{}, nil, nil
}

// guessType heuristically maps a model id to a category. OpenAI's
// /models lumps everything together with no `type` field; we sort
// chat / image / embedding / stt / tts so the UI tabs work.
func guessType(id string) string {
	low := strings.ToLower(id)
	switch {
	case strings.Contains(low, "embed"):
		return ModelTypeEmbedding
	case strings.Contains(low, "whisper"), strings.Contains(low, "transcrib"):
		return ModelTypeSTT
	case strings.Contains(low, "tts"), strings.Contains(low, "voice"):
		return ModelTypeTTS
	case strings.Contains(low, "image"),
		strings.Contains(low, "dall-e"),
		strings.Contains(low, "dalle"),
		strings.Contains(low, "flux"):
		return ModelTypeImage
	case strings.Contains(low, "video"), strings.Contains(low, "sora"):
		return ModelTypeVideo
	default:
		return ModelTypeChat
	}
}

// ─── Google Gemini ──────────────────────────────────────

func fetchGoogle(ctx context.Context, base, key string, userID uuid.UUID, providerID string,
) ([]ModelInput, error) {
	endpoint := strings.TrimRight(base, "/") + "/models?key=" + url.QueryEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	body, err := doRequest(req)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Models []struct {
			Name             string `json:"name"`         // e.g. "models/gemini-1.5-pro"
			DisplayName      string `json:"displayName"`
			InputTokenLimit  int    `json:"inputTokenLimit"`
			SupportedMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("google /models: %w", err)
	}
	out := make([]ModelInput, 0, len(parsed.Models))
	for i, m := range parsed.Models {
		// Strip "models/" prefix to get the wire id we use elsewhere.
		id := strings.TrimPrefix(m.Name, "models/")
		if id == "" {
			continue
		}
		// Filter to chat-capable models only — Gemini /models also
		// includes embedding / aqa rows that don't go through generateContent.
		isChat := false
		for _, sm := range m.SupportedMethods {
			if sm == "generateContent" || sm == "streamGenerateContent" {
				isChat = true
				break
			}
		}
		if !isChat {
			continue
		}
		display := m.DisplayName
		if display == "" {
			display = id
		}
		var cw *int
		if m.InputTokenLimit > 0 {
			v := m.InputTokenLimit
			cw = &v
		}
		out = append(out, ModelInput{
			UserID:        userID,
			ProviderID:    providerID,
			ModelID:       id,
			DisplayName:   display,
			Type:          ModelTypeChat,
			Abilities:     map[string]bool{"vision": true, "functions": true},
			ContextWindow: cw,
			SortOrder:     i,
			Source:        ModelSourceRemote,
		})
	}
	return out, nil
}

// ─── HTTP helper ────────────────────────────────────────

func doRequest(req *http.Request) ([]byte, error) {
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d: %s",
			resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

