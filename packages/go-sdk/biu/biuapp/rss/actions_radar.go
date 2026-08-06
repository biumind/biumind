// Radar action handlers — keyword rules + hits CRUD on rss.watch_*
// schema. Wired only when the App was constructed via NewWithPool
// AND a RadarStore was attached via WithRadar.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// RuleSummary is the SDK-side projection of a watch_rules row.
type RuleSummary struct {
	ID                uuid.UUID
	Scope             string
	ScopeID           string
	Name              string
	MatchAny          []string
	MatchAll          []string
	Exclude           []string
	Sources           []string
	OnHitBadge        string
	OnHitNotify       []string
	CooldownSec       int
	Enabled           bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	SemanticQuery     string
	SemanticThreshold float32
	Actions           []byte // raw jsonb
}

// HitSummary is the SDK-side projection of a watch_hits row.
type HitSummary struct {
	ID       int64
	RuleID   uuid.UUID
	HitAt    time.Time
	Source   string
	Title    string
	URL      string
	Notified bool
	Read     bool
	// Snapshot of (rule.name, rule.on_hit_badge) at fire time, joined
	// in by the store so the UI can render the timeline without
	// loading rules.
	RuleName     string
	HitSeverity  string
}

// RadarStore is the SDK-side surface for rules + hits CRUD. The
// app_center layer adapts its concrete radar.Store to this.
type RadarStore interface {
	CreateRule(ctx context.Context, scope, scopeID string, in CreateRuleInput) (*RuleSummary, error)
	ListRules(ctx context.Context, scope, scopeID string) ([]*RuleSummary, error)
	UpdateRule(ctx context.Context, scope, scopeID string, id uuid.UUID, in UpdateRuleInput) (*RuleSummary, error)
	DeleteRule(ctx context.Context, scope, scopeID string, id uuid.UUID) error

	ListHits(ctx context.Context, scope, scopeID string, opts ListHitsOpts) ([]*HitSummary, error)
	MarkHitRead(ctx context.Context, scope, scopeID string, id int64) error
	UnreadCount(ctx context.Context, scope, scopeID string) (int, error)
	UnreadMaxSeverity(ctx context.Context, scope, scopeID string) (string, error)
}

type CreateRuleInput struct {
	Name              string
	MatchAny          []string
	MatchAll          []string
	Exclude           []string
	Sources           []string
	OnHitBadge        string
	OnHitNotify       []string
	CooldownSec       int
	SemanticQuery     string
	SemanticThreshold float32
	Actions           []byte
}

type UpdateRuleInput struct {
	Name              *string
	MatchAny          *[]string
	MatchAll          *[]string
	Exclude           *[]string
	Sources           *[]string
	OnHitBadge        *string
	OnHitNotify       *[]string
	CooldownSec       *int
	Enabled           *bool
	SemanticQuery     *string
	SemanticThreshold *float32
	Actions           *[]byte
}

type ListHitsOpts struct {
	RuleID     uuid.UUID
	UnreadOnly bool
	Limit      int
}

// ─── action handlers ──────────────────────────────────────────────

func (a *App) invokeRulesCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.radar == nil {
		return nil, errors.New("rss: radar not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), true)
	if err != nil {
		return nil, err
	}
	var in struct {
		Name              string          `json:"name"`
		MatchAny          []string        `json:"match_any,omitempty"`
		MatchAll          []string        `json:"match_all,omitempty"`
		Exclude           []string        `json:"exclude,omitempty"`
		Sources           []string        `json:"sources,omitempty"`
		OnHitBadge        string          `json:"on_hit_badge,omitempty"`
		OnHitNotify       []string        `json:"on_hit_notify,omitempty"`
		CooldownSec       int             `json:"cooldown_sec,omitempty"`
		SemanticQuery     string          `json:"semantic_query,omitempty"`
		SemanticThreshold float32         `json:"semantic_threshold,omitempty"`
		Actions           json.RawMessage `json:"actions,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.Name == "" {
		return nil, errors.New("rss: rule name required")
	}
	r, err := a.radar.CreateRule(ctx, scope, scopeID, CreateRuleInput{
		Name: in.Name, MatchAny: in.MatchAny, MatchAll: in.MatchAll,
		Exclude: in.Exclude, Sources: in.Sources,
		OnHitBadge: in.OnHitBadge, OnHitNotify: in.OnHitNotify,
		CooldownSec:       in.CooldownSec,
		SemanticQuery:     in.SemanticQuery,
		SemanticThreshold: in.SemanticThreshold,
		Actions:           []byte(in.Actions),
	})
	if err != nil {
		return nil, err
	}
	// M8.2: rule 创建后, 若 SemanticQuery 非空且 embedQuery 已注入,
	// 算 query embedding 并 persist. 失败不阻塞 rule 创建 — keyword 路径
	// 仍工作, 雷达 cosine 下次重算或 admin 重新保存即可.
	if in.SemanticQuery != "" && a.embedQuery != nil {
		_ = a.syncRuleEmbedding(ctx, r.ID, in.SemanticQuery)
	}
	return ruleJSON(r), nil
}

func (a *App) invokeRulesList(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.radar == nil {
		return nil, errors.New("rss: radar not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), false)
	if err != nil {
		return nil, err
	}
	rs, err := a.radar.ListRules(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(rs))
	for i, r := range rs {
		items[i] = ruleJSON(r)
	}
	return map[string]any{"items": items}, nil
}

func (a *App) invokeRulesUpdate(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.radar == nil {
		return nil, errors.New("rss: radar not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), true)
	if err != nil {
		return nil, err
	}
	var in struct {
		ID                string           `json:"id"`
		Name              *string          `json:"name,omitempty"`
		MatchAny          *[]string        `json:"match_any,omitempty"`
		MatchAll          *[]string        `json:"match_all,omitempty"`
		Exclude           *[]string        `json:"exclude,omitempty"`
		Sources           *[]string        `json:"sources,omitempty"`
		OnHitBadge        *string          `json:"on_hit_badge,omitempty"`
		OnHitNotify       *[]string        `json:"on_hit_notify,omitempty"`
		CooldownSec       *int             `json:"cooldown_sec,omitempty"`
		Enabled           *bool            `json:"enabled,omitempty"`
		SemanticQuery     *string          `json:"semantic_query,omitempty"`
		SemanticThreshold *float32         `json:"semantic_threshold,omitempty"`
		Actions           *json.RawMessage `json:"actions,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad rule id: %w", err)
	}
	uin := UpdateRuleInput{
		Name: in.Name, MatchAny: in.MatchAny, MatchAll: in.MatchAll,
		Exclude: in.Exclude, Sources: in.Sources,
		OnHitBadge: in.OnHitBadge, OnHitNotify: in.OnHitNotify,
		CooldownSec: in.CooldownSec, Enabled: in.Enabled,
		SemanticQuery: in.SemanticQuery, SemanticThreshold: in.SemanticThreshold,
	}
	if in.Actions != nil {
		raw := []byte(*in.Actions)
		uin.Actions = &raw
	}
	r, err := a.radar.UpdateRule(ctx, scope, scopeID, id, uin)
	if err != nil {
		return nil, err
	}
	// M8.2: 若本次 update 改了 SemanticQuery, 重算 embedding. SemanticQuery
	// 为 nil 表示未传该字段(不动); 非 nil 但空字符串表示 explicit 清空(应清
	// embedding); 非空字符串则重算.
	if in.SemanticQuery != nil && a.embedQuery != nil {
		newQuery := strings.TrimSpace(*in.SemanticQuery)
		if newQuery == "" {
			_ = a.clearRuleEmbedding(ctx, r.ID)
		} else {
			_ = a.syncRuleEmbedding(ctx, r.ID, newQuery)
		}
	}
	return ruleJSON(r), nil
}

func (a *App) invokeRulesDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.radar == nil {
		return nil, errors.New("rss: radar not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), true)
	if err != nil {
		return nil, err
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad rule id: %w", err)
	}
	if err := a.radar.DeleteRule(ctx, scope, scopeID, id); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) invokeHitsList(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.radar == nil {
		return nil, errors.New("rss: radar not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), false)
	if err != nil {
		return nil, err
	}
	var in struct {
		RuleID     string `json:"rule_id,omitempty"`
		UnreadOnly bool   `json:"unread_only,omitempty"`
		Limit      int    `json:"limit,omitempty"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("rss: bad input: %w", err)
		}
	}
	opts := ListHitsOpts{UnreadOnly: in.UnreadOnly, Limit: in.Limit}
	if in.RuleID != "" {
		rid, err := uuid.Parse(in.RuleID)
		if err != nil {
			return nil, fmt.Errorf("rss: bad rule id: %w", err)
		}
		opts.RuleID = rid
	}
	hits, err := a.radar.ListHits(ctx, scope, scopeID, opts)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(hits))
	for i, h := range hits {
		items[i] = hitJSON(h)
	}
	return map[string]any{"items": items}, nil
}

func (a *App) invokeHitsMarkRead(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.radar == nil {
		return nil, errors.New("rss: radar not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), false)
	if err != nil {
		return nil, err
	}
	var in struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.ID == 0 {
		return nil, errors.New("rss: hit id required")
	}
	if err := a.radar.MarkHitRead(ctx, scope, scopeID, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) invokeRadarCount(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.radar == nil {
		return nil, errors.New("rss: radar not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), false)
	if err != nil {
		return nil, err
	}
	n, err := a.radar.UnreadCount(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	severity, err := a.radar.UnreadMaxSeverity(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	if severity == "" {
		severity = "info"
	}
	return map[string]any{"count": n, "severity": severity}, nil
}

// ─── JSON projections ─────────────────────────────────────────────

func ruleJSON(r *RuleSummary) map[string]any {
	out := map[string]any{
		"id":            r.ID.String(),
		"name":          r.Name,
		"match_any":     r.MatchAny,
		"match_all":     r.MatchAll,
		"exclude":       r.Exclude,
		"sources":       r.Sources,
		"on_hit_badge":  r.OnHitBadge,
		"on_hit_notify": r.OnHitNotify,
		"cooldown_sec":  r.CooldownSec,
		"enabled":       r.Enabled,
		"created_at":    r.CreatedAt,
	}
	if r.SemanticQuery != "" {
		out["semantic_query"] = r.SemanticQuery
		out["semantic_threshold"] = r.SemanticThreshold
	}
	if len(r.Actions) > 0 && string(r.Actions) != "[]" {
		// raw json passthrough — clients decode themselves.
		out["actions"] = json.RawMessage(r.Actions)
	}
	return out
}

func hitJSON(h *HitSummary) map[string]any {
	out := map[string]any{
		"id":          h.ID,
		"rule_id":     h.RuleID.String(),
		"rule_name":   h.RuleName,
		"hit_at":      h.HitAt,
		"source":      h.Source,
		"title":       h.Title,
		"url":         h.URL,
		"notified":    h.Notified,
		"unread":      !h.Read,
		"severity":    h.HitSeverity,
	}
	if h.HitSeverity == "error" {
		out["severity_label"] = "🚨 紧急"
	} else if h.HitSeverity == "warn" {
		out["severity_label"] = "⚠️ 警告"
	} else {
		out["severity_label"] = ""
	}
	return out
}

// ─── M9 action_runs_list ───────────────────────────────────────────

func (a *App) invokeActionRunsList(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	scope, scopeID, err := a.resolveScope(ctx, scopeOf(raw), false)
	if err != nil {
		return nil, err
	}
	var in struct {
		RuleID string `json:"rule_id"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	rid, err := uuid.Parse(in.RuleID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad rule_id: %w", err)
	}
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// 跨用户保护: 先验 rule.scope = caller scope, 防止用户拿别人的 rule_id 看历史
	var rScope, rScopeID string
	if err := a.pg.pool.QueryRow(ctx,
		`SELECT scope, scope_id FROM rss.watch_rules WHERE id=$1`, rid).
		Scan(&rScope, &rScopeID); err != nil {
		return nil, fmt.Errorf("rss: rule not found: %w", err)
	}
	if rScope != scope || rScopeID != scopeID {
		return nil, errors.New("rss: rule belongs to a different scope")
	}

	rows, err := a.pg.pool.Query(ctx, `
		SELECT id, hit_id, action_seq, action_type, status,
		       COALESCE(result::text, ''), error, started_at, duration_ms
		  FROM rss.action_runs
		 WHERE rule_id = $1
		 ORDER BY started_at DESC
		 LIMIT $2`, rid, limit)
	if err != nil {
		return nil, fmt.Errorf("rss: action_runs query: %w", err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id         int64
			hitID      *int64
			seq        int
			atype      string
			status     string
			resultStr  string
			errStr     string
			startedAt  time.Time
			durationMs int
		)
		if err := rows.Scan(&id, &hitID, &seq, &atype, &status, &resultStr,
			&errStr, &startedAt, &durationMs); err != nil {
			return nil, fmt.Errorf("rss: action_runs scan: %w", err)
		}
		row := map[string]any{
			"id":          id,
			"action_seq":  seq,
			"action_type": atype,
			"status":      status,
			"started_at":  startedAt,
			"duration_ms": durationMs,
		}
		if hitID != nil {
			row["hit_id"] = *hitID
		}
		if errStr != "" {
			row["error"] = errStr
		}
		if resultStr != "" {
			var result map[string]any
			if json.Unmarshal([]byte(resultStr), &result) == nil {
				row["result"] = result
			}
		}
		items = append(items, row)
	}
	return map[string]any{"items": items}, rows.Err()
}

// ─── M8.2 cosine 雷达 ─────────────────────────────────────────────

// syncRuleEmbedding 算 query embedding 并 persist 到 watch_rules.
// 失败只记 log, 不返错 — caller 已经保过 rule 主体, embedding 是
// 增量优化, 下一轮 admin 编辑或定时重算即可补上.
func (a *App) syncRuleEmbedding(ctx context.Context, ruleID uuid.UUID, query string) error {
	if a.embedQuery == nil || a.pg == nil {
		return nil
	}
	vec, modelCode, err := a.embedQuery(ctx, query)
	if err != nil || len(vec) == 0 {
		return err
	}
	v := pgvector.NewVector(vec)
	_, err = a.pg.pool.Exec(ctx, `
		UPDATE rss.watch_rules
		   SET semantic_embedding=$2,
		       semantic_embedding_model=$3,
		       updated_at=now()
		 WHERE id=$1`, ruleID, v, modelCode)
	return err
}

// clearRuleEmbedding — semantic_query 被清空时调; 防止旧 embedding 继续
// 被 cosine matcher 取到产生误命中.
func (a *App) clearRuleEmbedding(ctx context.Context, ruleID uuid.UUID) error {
	if a.pg == nil {
		return nil
	}
	_, err := a.pg.pool.Exec(ctx, `
		UPDATE rss.watch_rules
		   SET semantic_embedding=NULL,
		       semantic_embedding_model=NULL,
		       updated_at=now()
		 WHERE id=$1`, ruleID)
	return err
}
