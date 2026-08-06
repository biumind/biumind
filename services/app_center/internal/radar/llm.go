// Natural-language → keyword rule via model-relay.
//
// Calls /v1/messages (Anthropic-compatible, BiuMind's primary LLM
// surface) with a structured-output prompt. The LLM returns JSON
// describing match_any/match_all/exclude/badge/cooldown which we
// project onto CreateRuleInput.
//
// Token: passes through the caller's bearer token from context, so
// quotas + per-user billing apply naturally — no service-wide key.

package radar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	llmModel        = "claude-haiku-4-5"
	llmMaxTokens    = 600
	llmTimeout      = 25 * time.Second
	llmSystemPrompt = `你是 BiuMind 雷达的关键词规则助手。
用户用一句话描述他想监控的资讯，你输出严格的 JSON，描述一条关键词命中规则。

JSON 字段：
  "name":         规则的简短名称（中文，6-15 字）
  "match_any":    任一关键词命中即通过的列表（建议 2-6 个，覆盖同一概念的常见说法）
  "match_all":    必须全部命中的关键词（用得少；只有当用户明显表达"必须同时出现 X 和 Y"时才填）
  "exclude":      否决关键词（招聘 / 实习 / 广告 / 八卦等，看用户语义补常见噪声）
  "on_hit_badge": "info" | "warn" | "error"。默认 "warn"；用户说"立刻通知/紧急"用 "error"；说"留意一下"用 "info"
  "cooldown_sec": 冷却秒数。默认 1800；用户说"立刻通知"或"重大事件"用 0；"汇总型"用 3600

只输出 JSON，不要解释、不要 Markdown 代码块、不要前后文字。

例子：

输入：凡是 OpenAI / Anthropic 发布新模型的事都通知我，不要招聘信息
输出：{"name":"AI 新模型发布","match_any":["OpenAI","Anthropic","GPT","Claude","新模型","发布"],"match_all":[],"exclude":["招聘","实习","intern"],"on_hit_badge":"warn","cooldown_sec":1800}

输入：美国对华芯片出口管制有任何变动立刻告诉我
输出：{"name":"美国芯片出口管制","match_any":["美国","芯片","半导体"],"match_all":["出口管制"],"exclude":[],"on_hit_badge":"error","cooldown_sec":0}

输入：苹果发布会、新 iPhone、新 Vision Pro
输出：{"name":"苹果硬件动态","match_any":["苹果","Apple","iPhone","Vision Pro","发布会","WWDC"],"match_all":[],"exclude":["维修","二手"],"on_hit_badge":"warn","cooldown_sec":1800}

输入：和 BiuMind 相关的任何消息
输出：{"name":"BiuMind 监控","match_any":["BiuMind","biumind","BiuApp"],"match_all":[],"exclude":[],"on_hit_badge":"error","cooldown_sec":0}`
)

var ErrLLMUnavailable = errors.New("radar: llm relay not configured")

type LLMClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewLLMClient(baseURL string) *LLMClient {
	return &LLMClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: llmTimeout},
	}
}

// LLMRule mirrors what the prompt asks the model to emit. Caller
// projects to CreateRuleInput after validation.
type LLMRule struct {
	Name        string   `json:"name"`
	MatchAny    []string `json:"match_any"`
	MatchAll    []string `json:"match_all"`
	Exclude     []string `json:"exclude"`
	OnHitBadge  string   `json:"on_hit_badge"`
	CooldownSec int      `json:"cooldown_sec"`
}

// FromNL turns a free-text description into an LLMRule. token is the
// caller's bearer (model-relay validates + bills). Two failure modes
// you should map at the API layer:
//
//   - ErrLLMUnavailable     → BaseURL empty (env not set)
//   - any other error       → upstream / parse failure; UI should
//                             toast and let the user fall back to
//                             manual chip entry.
func (c *LLMClient) FromNL(ctx context.Context, token, text string) (*LLMRule, error) {
	if c == nil || c.BaseURL == "" {
		return nil, ErrLLMUnavailable
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("radar: empty nl text")
	}

	body := map[string]any{
		"model":      llmModel,
		"max_tokens": llmMaxTokens,
		"system":     llmSystemPrompt,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": text,
			},
		},
	}
	buf, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("radar: build llm req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("radar: llm call: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("radar: llm status %d: %s", resp.StatusCode,
			truncate(string(respBytes), 200))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, fmt.Errorf("radar: llm decode: %w", err)
	}
	var raw string
	for _, c := range out.Content {
		if c.Type == "text" {
			raw += c.Text
		}
	}
	rule, err := parseLLMRule(raw)
	if err != nil {
		return nil, fmt.Errorf("radar: llm json: %w (raw=%q)", err, truncate(raw, 200))
	}
	return rule, nil
}

// parseLLMRule extracts the first JSON object from raw model output
// (defensive: even with a strict prompt, the model occasionally
// wraps in ``` fences or prefixes "JSON:") and validates the fields.
func parseLLMRule(raw string) (*LLMRule, error) {
	s := strings.TrimSpace(raw)
	// Strip Markdown fences.
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx > 0 {
			s = s[idx+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx > 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	// Find first {...} block.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return nil, errors.New("no json object")
	}
	s = s[start : end+1]

	var r LLMRule
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, err
	}
	r.Name = strings.TrimSpace(r.Name)
	r.MatchAny = cleanStrSlice(r.MatchAny)
	r.MatchAll = cleanStrSlice(r.MatchAll)
	r.Exclude = cleanStrSlice(r.Exclude)
	r.OnHitBadge = strings.ToLower(strings.TrimSpace(r.OnHitBadge))
	switch r.OnHitBadge {
	case "info", "warn", "error":
	default:
		r.OnHitBadge = "warn"
	}
	if r.CooldownSec < 0 {
		r.CooldownSec = 0
	}
	if r.CooldownSec > 86400 {
		r.CooldownSec = 86400
	}
	if len(r.MatchAny) == 0 && len(r.MatchAll) == 0 {
		return nil, errors.New("rule has no keywords")
	}
	if r.Name == "" {
		// Fall back to first keyword as name when LLM omits.
		if len(r.MatchAny) > 0 {
			r.Name = r.MatchAny[0]
		} else {
			r.Name = r.MatchAll[0]
		}
	}
	return &r, nil
}

func cleanStrSlice(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Rephrase rewrites the given article body in a persona's voice. The
// system prompt is built fresh per call (no JSON contract — output is
// free-form prose); response is the model's full text. Token caps:
// input ≤ ~4 k chars, output ≤ ~250 tokens.
func (c *LLMClient) Rephrase(ctx context.Context, token, title, personaPrompt, body string) (string, error) {
	if c == nil || c.BaseURL == "" {
		return "", ErrLLMUnavailable
	}
	if strings.TrimSpace(personaPrompt) == "" || strings.TrimSpace(body) == "" {
		return "", errors.New("radar: empty rephrase input")
	}

	system := "你是 BiuMind 阅读助手。把用户给的文章用以下风格重写成一段 ≤ 200 字的中文 (除非要求英文): " +
		personaPrompt + " 直接输出, 不要前后解释, 不要 Markdown 列表。"

	bodyJSON, _ := json.Marshal(map[string]any{
		"model":      llmModel,
		"max_tokens": 350,
		"system":     system,
		"messages": []map[string]any{
			{"role": "user", "content": "标题: " + title + "\n\n正文:\n" + body},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/messages", bytes.NewReader(bodyJSON))
	if err != nil {
		return "", fmt.Errorf("radar: rephrase req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("radar: rephrase transport: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("radar: rephrase status %d: %s", resp.StatusCode,
			truncate(string(respBytes), 200))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("radar: rephrase decode: %w", err)
	}
	var text string
	for _, c := range out.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	return strings.TrimSpace(text), nil
}
