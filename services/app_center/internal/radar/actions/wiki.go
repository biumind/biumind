// wiki Action — radar 命中后追加一条到用户 Wiki. 跟 entries_to_wiki 不同:
// 后者沉一篇完整文章 (entry_id 已知 + AI digest 已跑); radar wiki 只是
// 命中提醒, 内容是 (rule_name, hit.title, hit.url, hit_at), 适合做
// "周末翻一翻" 的 brain dump.
//
// config shape (可选):
//   { "page_path": "信息流/雷达/{rule_name}",   // 默认 "信息流/雷达/{rule_name}"
//     "include_url": true }
//
// 实现细节:
//   - 通过 WikiAppender 接口 (services 层注入), 不在 actions 包里直接
//     拿 HTTP client. 让单测可 mock.
//   - 失败不重试 — 雷达 hit 时间敏感, 翻天覆地的可靠性留给 entries_to_wiki

package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/biumind/biumind/services/app_center/internal/radar"
)

// WikiAppender — services 层注入的写入实现. ctx 必须带 caller bearer
// 让 brain 能 scope 到具体用户.
type WikiAppender interface {
	// AppendNote 追加一条 markdown 块到用户的 wiki page.
	// pagePath 形如 "信息流/雷达/AI 监控"; 不存在的中间路径要自动建.
	// 返 (page_id, block_id) 给 action_runs.result 用.
	AppendNote(ctx context.Context, userID string, pagePath string, markdown string) (pageID string, blockID string, err error)
}

type WikiConfig struct {
	PagePath   string `json:"page_path,omitempty"`
	IncludeURL bool   `json:"include_url,omitempty"`
}

type WikiAction struct {
	Appender WikiAppender
}

func NewWiki(a WikiAppender) *WikiAction { return &WikiAction{Appender: a} }

func (WikiAction) Type() string { return "wiki" }

func (a *WikiAction) Run(ctx context.Context, hit *radar.Hit, configRaw json.RawMessage) (Result, error) {
	if a.Appender == nil {
		return nil, errors.New("wiki: appender not wired")
	}
	if hit.RuleSnapshot.Scope != "user" {
		return nil, fmt.Errorf("wiki: only user-scope rules supported (got %s)", hit.RuleSnapshot.Scope)
	}

	cfg := WikiConfig{IncludeURL: true}
	if len(configRaw) > 0 {
		if err := json.Unmarshal(configRaw, &cfg); err != nil {
			return nil, fmt.Errorf("wiki: parse config: %w", err)
		}
	}
	pagePath := cfg.PagePath
	if pagePath == "" {
		pagePath = "信息流/雷达/" + hit.RuleSnapshot.Name
	}

	// markdown 块: 一行命中信息, 时间戳让事后翻看时间序更直观.
	md := fmt.Sprintf("- **%s** · %s",
		hit.HitAt.Format("2006-01-02 15:04"), hit.Title)
	if cfg.IncludeURL && hit.URL != "" {
		md += fmt.Sprintf(" — [link](%s)", hit.URL)
	}

	pageID, blockID, err := a.Appender.AppendNote(ctx, hit.RuleSnapshot.ScopeID, pagePath, md)
	if err != nil {
		return nil, fmt.Errorf("wiki: append: %w", err)
	}
	return Result{
		"page_id":   pageID,
		"block_id":  blockID,
		"page_path": pagePath,
	}, nil
}
