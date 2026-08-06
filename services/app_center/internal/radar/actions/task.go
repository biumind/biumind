// task Action — 雷达命中创建一条 todo (走 tasks app).
//
// 调 internal HTTP: POST /v1/apps/tasks/invoke action=add input={...}.
// 不直 import tasks 包是为了让 actions 包跟 tasks 解耦, tasks app 重写
// 时不要重新编译 radar.
//
// config shape:
//   { "due_offset_days": 3,  // 默认 3 天后到期
//     "priority": "normal" }  // 'low' | 'normal' | 'high'

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

type TaskConfig struct {
	DueOffsetDays int    `json:"due_offset_days,omitempty"`
	Priority      string `json:"priority,omitempty"`
}

// TaskInvoker — services 注入的 invoke 客户端. 它知道如何拿到 caller
// 的 bearer (per-user JWT) 来打 /v1/apps/tasks/invoke.
type TaskInvoker interface {
	// InvokeTaskAdd 调 tasks app 的 add action, 返 created task id.
	// userID 用于在 invoker 内部签 per-user JWT.
	InvokeTaskAdd(ctx context.Context, userID string, payload map[string]any) (string, error)
}

type TaskAction struct {
	Invoker TaskInvoker
}

func NewTask(inv TaskInvoker) *TaskAction { return &TaskAction{Invoker: inv} }

func (TaskAction) Type() string { return "task" }

func (a *TaskAction) Run(ctx context.Context, hit *radar.Hit, configRaw json.RawMessage) (Result, error) {
	if a.Invoker == nil {
		return nil, errors.New("task: invoker not wired")
	}
	if hit.RuleSnapshot.Scope != "user" {
		return nil, fmt.Errorf("task: only user-scope rules supported (got %s)", hit.RuleSnapshot.Scope)
	}
	cfg := TaskConfig{DueOffsetDays: 3, Priority: "normal"}
	if len(configRaw) > 0 {
		if err := json.Unmarshal(configRaw, &cfg); err != nil {
			return nil, fmt.Errorf("task: parse config: %w", err)
		}
	}

	payload := map[string]any{
		"title":           fmt.Sprintf("[%s] %s", hit.RuleSnapshot.Name, hit.Title),
		"due_offset_days": cfg.DueOffsetDays,
		"priority":        cfg.Priority,
		"link":            hit.URL,
		"source":          hit.Source,
	}
	taskID, err := a.Invoker.InvokeTaskAdd(ctx, hit.RuleSnapshot.ScopeID, payload)
	if err != nil {
		return nil, fmt.Errorf("task: invoke add: %w", err)
	}
	return Result{
		"task_id":  taskID,
		"priority": cfg.Priority,
	}, nil
}

// HTTPTaskInvoker — 默认实现: POST <appCenterURL>/v1/apps/tasks/invoke.
// signFor 拿 user JWT 透传给 app_center 自己 (loop-back).
type HTTPTaskInvoker struct {
	BaseURL string // "http://app-center:7011"
	HTTP    *http.Client
	SignFor func(userID string) (string, error)
}

func (h *HTTPTaskInvoker) InvokeTaskAdd(ctx context.Context, userID string, payload map[string]any) (string, error) {
	if h.HTTP == nil {
		return "", errors.New("task: http client nil")
	}
	if h.SignFor == nil {
		return "", errors.New("task: signFor nil (per-user JWT 必须)")
	}
	token, err := h.SignFor(userID)
	if err != nil {
		return "", fmt.Errorf("task: sign user token: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"action": "add",
		"args":   payload,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.BaseURL+"/v1/apps/tasks/invoke", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := h.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("task: status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var out struct {
		Result map[string]any `json:"result"`
		ID     string         `json:"id"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("task: decode: %w", err)
	}
	if id, ok := out.Result["id"].(string); ok && id != "" {
		return id, nil
	}
	if out.ID != "" {
		return out.ID, nil
	}
	return "", nil // tasks app 可能不返 id; 不当错处理
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
