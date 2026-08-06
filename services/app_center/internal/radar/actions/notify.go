// notify Action — emit realtime event + (optionally) push to configured
// notify channels. Wraps the v2 dispatcher's hard-coded notify path so
// it can run as one element in rule.Actions[].
//
// config shape (all optional; falls back to rule fields):
//   { "channels": ["feishu_xxx", "bark_yyy"], "include_url": true }
//
// 没传 config 时退化到 rule.OnHitNotify 字段 (向后兼容 v2 rule).

package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/biumind/biumind/services/app_center/internal/radar"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotifyConfig struct {
	Channels   []string `json:"channels,omitempty"`
	IncludeURL bool     `json:"include_url,omitempty"`
}

// Notifier — 复用 dispatcher 已经定义的接口, 不重新引一份避免 pkg
// 循环依赖. dispatcher 把它的 Notifier 透传过来即可.
type Notifier = radar.Notifier

type NotifyAction struct {
	Pool     *pgxpool.Pool
	Notifier Notifier // 可 nil — 此时只发 events 不外推
	Logger   *slog.Logger
}

func NewNotify(pool *pgxpool.Pool, n Notifier, logger *slog.Logger) *NotifyAction {
	if logger == nil {
		logger = slog.Default()
	}
	return &NotifyAction{Pool: pool, Notifier: n, Logger: logger}
}

func (NotifyAction) Type() string { return "notify" }

func (a *NotifyAction) Run(ctx context.Context, hit *radar.Hit, configRaw json.RawMessage) (Result, error) {
	cfg := NotifyConfig{}
	if len(configRaw) > 0 {
		if err := json.Unmarshal(configRaw, &cfg); err != nil {
			return nil, fmt.Errorf("notify: parse config: %w", err)
		}
	}
	channels := cfg.Channels
	if len(channels) == 0 {
		channels = hit.RuleSnapshot.OnHitNotify // v2 fallback
	}

	// 1. realtime event (radar.hit_fired) — 跟 dispatcher v1 同样行为.
	payload := map[string]any{
		"hit_id":    hit.ID,
		"rule_id":   hit.RuleID.String(),
		"rule_name": hit.RuleSnapshot.Name,
		"severity":  hit.RuleSnapshot.OnHitBadge,
		"source":    hit.Source,
		"title":     hit.Title,
		"url":       hit.URL,
		"hit_at":    hit.HitAt,
	}
	if err := events.Write(ctx, a.Pool, events.Event{
		ScopeKind: hit.RuleSnapshot.Scope,
		ScopeID:   hit.RuleSnapshot.ScopeID,
		ActorType: events.ActorScheduler,
		ActorID:   "radar",
		Type:      events.RadarHitFired,
		Payload:   payload,
	}); err != nil {
		// event 写失败不致命 — 推送仍可进行; 但要返 error 让 action_runs 记下来
		a.Logger.Warn("notify: event write", "hit_id", hit.ID, "err", err.Error())
	}

	// 2. external channel fanout
	pushed := 0
	if a.Notifier != nil {
		msg := radar.NotifyMessage{
			Title:    fmt.Sprintf("[%s] %s", hit.RuleSnapshot.Name, hit.Source),
			Body:     hit.Title,
			URL:      hit.URL,
			Severity: hit.RuleSnapshot.OnHitBadge,
			RuleName: hit.RuleSnapshot.Name,
		}
		if !cfg.IncludeURL {
			msg.URL = ""
		}
		for _, ch := range channels {
			if err := a.Notifier.Send(ctx, ch, msg); err != nil {
				a.Logger.Warn("notify: send", "channel", ch, "hit_id", hit.ID, "err", err.Error())
				continue
			}
			pushed++
		}
	}

	return Result{
		"channels_total":  len(channels),
		"channels_pushed": pushed,
		"event_emitted":   true,
	}, nil
}
