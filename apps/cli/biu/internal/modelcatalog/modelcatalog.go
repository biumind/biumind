// Package modelcatalog reads the user-facing model catalog from
// model-relay (GET /v1/me/models, user JWT auth).
//
// 消费方是"零配置启动"链路: biu 启动时 [default].model 为空 → 拉目录
// 找 is_default_chat 标记的平台默认模型 (live 解析, 不落 config —— 与
// Flutter chat 的 systemDefault 语义一致, admin 换默认后自动跟随)。
// admin 未设默认时目录里没有标记, 由调用方引导用户手选 (/model)。
//
// 该端点免费、不耗 quota (见 services/model-relay internal/api/publicmodels.go)。

package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Model 是 /v1/me/models 单条 DTO 中 CLI 关心的子集。字段名对齐
// publicModelDTO (code / display_name / mode / min_plan / is_default_chat)。
type Model struct {
	Code        string `json:"code"`
	DisplayName string `json:"display_name"`
	Mode        string `json:"mode"`
	MinPlan     string `json:"min_plan"`
	IsDefault   bool   `json:"is_default_chat"`
}

// ErrNoDefault 表示目录里没有 is_default_chat 标记 —— admin 未设平台
// 默认模型, 调用方应引导用户手选而不是报错。
var ErrNoDefault = fmt.Errorf("no default chat model configured on the platform")

// List fetches the active model catalog. relayURL is the model-relay base
// (e.g. https://biumind.xxlab.tech), token a user JWT / virtual key.
func List(ctx context.Context, relayURL, token string) ([]Model, error) {
	url := strings.TrimRight(relayURL, "/") + "/v1/me/models?status=active"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model catalog: %s: %s", resp.Status, url)
	}
	var out struct {
		Items []Model `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// DefaultChat picks the platform default chat model off a fetched list.
// Returns ErrNoDefault when no row carries the flag.
func DefaultChat(models []Model) (string, error) {
	for _, m := range models {
		if m.IsDefault {
			return m.Code, nil
		}
	}
	return "", ErrNoDefault
}

// ChatModels filters the catalog down to chat-mode entries (what the
// REPL /model picker offers).
func ChatModels(models []Model) []Model {
	out := make([]Model, 0, len(models))
	for _, m := range models {
		if m.Mode == "chat" {
			out = append(out, m)
		}
	}
	return out
}
