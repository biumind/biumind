// copilot_ask — M9.4 同步 RSS Q&A. 客户端右抽屉一问一答, 后端注入 view
// 的 context, 调 model-relay /v1/messages 拿答案, 解析 [N] 引用映射回
// entry IDs.
//
// 不走 brain agent session — 抽屉式轻量交互, 不需要 thread / WS / tools.
// 一次性 RPC 拿完整 markdown 答案 + citations array. 客户端简单显示即可.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// CopilotAsker — services 注入的 LLM 客户端. 把 system_prompt + user
// question 打到 model-relay /v1/messages 拿完整答案.
type CopilotAsker interface {
	// Ask 同步调用. 返完整 answer markdown 字符串.
	// userID 让实现签 per-user JWT (跟 digest/embed 同模式).
	Ask(ctx context.Context, userID, systemPrompt, question string) (answer string, err error)
}

// CopilotContextBuilder — services 注入的 RSS context 拼接.
type CopilotContextBuilder interface {
	BuildContext(ctx context.Context, userID, viewKind, currentEntryID string) (
		systemPrompt string, items []CopilotItem, err error)
}

// CopilotItem — copilot 返客户端的引用映射. 跟 services/rss/copilot/inject.go
// 的 Item 同 shape.
type CopilotItem struct {
	N       int    `json:"n"`
	EntryID string `json:"entry_id"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Source  string `json:"source,omitempty"`
}

// WithCopilot — 注入两个依赖. nil 任一时 copilot_ask 返 "not wired".
func (a *App) WithCopilot(builder CopilotContextBuilder, asker CopilotAsker) *App {
	a.copilotBuilder = builder
	a.copilotAsker = asker
	return a
}

// citationRE — 匹配独立的 [N] (N=1..99). 不匹配 [link][1] 形式 (markdown
// reference link), 因为 system_prompt 里强制要求 LLM 不那么写.
var citationRE = regexp.MustCompile(`\[(\d{1,2})\]`)

func (a *App) invokeCopilotAsk(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.copilotAsker == nil || a.copilotBuilder == nil {
		return nil, errors.New("rss: copilot not wired (model-relay url 未配)")
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if scope != "user" {
		return nil, errors.New("rss: copilot only supports user scope")
	}

	var in struct {
		ViewKind       string `json:"view_kind"`
		Question       string `json:"question"`
		CurrentEntryID string `json:"current_entry_id,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.Question == "" {
		return nil, errors.New("rss: question required")
	}
	if in.ViewKind == "" {
		in.ViewKind = "today" // 默认 today
	}

	systemPrompt, items, err := a.copilotBuilder.BuildContext(
		ctx, scopeID, in.ViewKind, in.CurrentEntryID)
	if err != nil {
		return nil, fmt.Errorf("rss: build context: %w", err)
	}

	answer, err := a.copilotAsker.Ask(ctx, scopeID, systemPrompt, in.Question)
	if err != nil {
		return nil, fmt.Errorf("rss: ask llm: %w", err)
	}

	// 解析 [N] 引用 → 客户端只返实际被命中的 items 加快渲染.
	cited := extractCitations(answer, items)
	return map[string]any{
		"answer":     answer,
		"citations":  cited,
		"items_seen": len(items), // debug — context 里给了多少候选
		"view_kind":  in.ViewKind,
	}, nil
}

// extractCitations — 从 markdown 答案里抓出现的 [N], 映射回 items.
// 重复 [N] 只返一次, 按答案中首次出现顺序排序.
func extractCitations(answer string, items []CopilotItem) []CopilotItem {
	if len(items) == 0 {
		return nil
	}
	matches := citationRE.FindAllStringSubmatchIndex(answer, -1)
	if len(matches) == 0 {
		return nil
	}
	byN := make(map[int]CopilotItem, len(items))
	for _, it := range items {
		byN[it.N] = it
	}
	seen := make(map[int]int, len(items)) // n → first occurrence index
	type firstHit struct {
		n   int
		pos int
	}
	hits := make([]firstHit, 0, len(matches))
	for _, m := range matches {
		// m[2:4] 是第一个分组 (N) 的 start/end
		nStr := answer[m[2]:m[3]]
		n, err := strconv.Atoi(nStr)
		if err != nil {
			continue
		}
		if _, ok := byN[n]; !ok {
			continue // [N] 但 N 不在 items 里 — LLM 编的, 跳过
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = m[0]
		hits = append(hits, firstHit{n: n, pos: m[0]})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	out := make([]CopilotItem, 0, len(hits))
	for _, h := range hits {
		out = append(out, byN[h.n])
	}
	return out
}
