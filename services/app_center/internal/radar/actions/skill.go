// skill Action — 调 runtime 的 skill invoke (M9.1.5). 让用户写一条规则
// 命中即跑某个 skill — 比如 "Hacker News 头条 → translate skill 翻译并 push".
//
// runtime endpoint: POST <runtimeURL>/v1/tools/<skill_id>/invoke
// authz: app:invoke (调用方 bearer 由 SignFor 签)
//
// config shape:
//   { "skill_id": "<uuid>",
//     "input":    { ... 透传给 skill ...} }
//
// 雷达字段会作为 _hit 注入 input 顶层方便 skill 引用.

package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/biumind/biumind/services/app_center/internal/radar"
)

type SkillConfig struct {
	SkillID string         `json:"skill_id"`
	Input   map[string]any `json:"input,omitempty"`
}

type SkillInvoker interface {
	// InvokeSkill — POST runtime /v1/tools/{skill_id}/invoke. 返
	// raw response body (skill 自由 shape).
	InvokeSkill(ctx context.Context, userID, skillID string, input map[string]any) (json.RawMessage, error)
}

type SkillAction struct {
	Invoker SkillInvoker
}

func NewSkill(inv SkillInvoker) *SkillAction { return &SkillAction{Invoker: inv} }

func (SkillAction) Type() string { return "skill" }

func (a *SkillAction) Run(ctx context.Context, hit *radar.Hit, configRaw json.RawMessage) (Result, error) {
	if a.Invoker == nil {
		return nil, errors.New("skill: invoker not wired")
	}
	if hit.RuleSnapshot.Scope != "user" {
		return nil, fmt.Errorf("skill: only user-scope rules supported (got %s)", hit.RuleSnapshot.Scope)
	}
	var cfg SkillConfig
	if len(configRaw) == 0 {
		return nil, errors.New("skill: config required")
	}
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return nil, fmt.Errorf("skill: parse config: %w", err)
	}
	if cfg.SkillID == "" {
		return nil, errors.New("skill: skill_id required")
	}

	input := make(map[string]any, len(cfg.Input)+1)
	for k, v := range cfg.Input {
		input[k] = v
	}
	input["_hit"] = map[string]any{
		"rule_id":   hit.RuleID.String(),
		"rule_name": hit.RuleSnapshot.Name,
		"title":     hit.Title,
		"url":       hit.URL,
		"source":    hit.Source,
		"severity":  hit.RuleSnapshot.OnHitBadge,
	}

	resp, err := a.Invoker.InvokeSkill(ctx, hit.RuleSnapshot.ScopeID, cfg.SkillID, input)
	if err != nil {
		return nil, fmt.Errorf("skill: invoke: %w", err)
	}
	out := Result{"skill_id": cfg.SkillID}
	if len(resp) > 0 && len(resp) < 4096 {
		// 短结果直接放 result jsonb 方便日志查询; 大结果只记 size
		var preview map[string]any
		if json.Unmarshal(resp, &preview) == nil {
			out["preview"] = preview
		}
	} else {
		out["response_size"] = len(resp)
	}
	return out, nil
}

// HTTPSkillInvoker — 默认实现.
type HTTPSkillInvoker struct {
	RuntimeURL string
	HTTP       *http.Client
	SignFor    func(userID string) (string, error)
}

func (h *HTTPSkillInvoker) InvokeSkill(ctx context.Context, userID, skillID string, input map[string]any) (json.RawMessage, error) {
	if h.HTTP == nil {
		return nil, errors.New("skill: http nil")
	}
	if h.SignFor == nil {
		return nil, errors.New("skill: signFor nil")
	}
	token, err := h.SignFor(userID)
	if err != nil {
		return nil, fmt.Errorf("skill: sign: %w", err)
	}
	body, _ := json.Marshal(map[string]any{"input": input})
	url := fmt.Sprintf("%s/v1/tools/%s/invoke", h.RuntimeURL, skillID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("skill: status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	return json.RawMessage(respBody), nil
}
