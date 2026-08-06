// Package rss — RSS / Atom 2.0 subscription manager exposed as a
// BiuApp. v1.5 hybrid form: backend (Go SDK) + declarative views
// (manifest.views) so the user sees a real UI in App Center, and
// the Agent calls the same actions as tools.
//
// Actions:
//
//	subscribe(url, title?, tags?[])    → {subscription_id, ...}
//	unsubscribe(id)                    → {ok: true}
//	list_subscriptions()               → {items: [...]}
//	fetch(url, limit?)                 → {title, items: [...]}
//	refresh_all()                      → {refreshed: int}
//	digest(window?, max_items?)        → {summary: text, source_count}
//
// We hand-parse the XML rather than pull a third-party library so
// the SDK stays dependency-free. Both classic RSS 2.0 (<rss>
// <channel>) and Atom (<feed>) are handled.
package rss

import (
	"context"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/jackc/pgx/v5/pgxpool"
)

// summarizeSkill is the SKILL.md body for the rss-summarize skill,
// embedded at compile time so the running service can write it into
// runtime.skills without filesystem access. Updating the .md file
// in skills/ requires a rebuild — that's intentional: skill content
// is part of the App's release surface and shouldn't drift between
// the binary and what's loaded.
//
//go:embed skills/summarize.md
var summarizeSkill []byte

const Name = "rss"

type App struct {
	httpClient *http.Client
	store      *store // legacy in-memory; nil when pg is wired

	pg       *PGStore    // PG-backed store; nil for in-memory mode
	sched    *Scheduler  // refresh worker (uses pg + fetcher)
	fetcher  Fetcher     // discovery + scheduled refresh
	boards   BoardsStore // optional rankings (P1); nil disables boards_*
	radar    RadarStore  // optional radar (P2); nil disables rules_*/hits_*
	llm      LLMAdvisor  // optional LLM advisor (P3); nil disables rules_from_nl
	today    TodayPicker // optional Today picker (M2); nil disables today_picks
	wiki     WikiSink    // optional wiki sink (M3); nil disables entries_to_wiki
	discover *Discoverer // M5 source kind detection + feed URL discovery

	// embedQuery — M8.2 cosine 雷达: 算 rule.semantic_query 的 embedding,
	// 写入 watch_rules.semantic_embedding. nil 时 rule 创建/更新仍工作,
	// 只是 cosine 路径不可用 (fallback 走 keyword/token-based 匹配).
	// 包装 model-relay /v1/embeddings; 接线在 app_center main.go.
	embedQuery EmbedQueryFn

	// briefing — M8.4 Today TTS 简报合成器. nil 时 briefing_today_audio
	// action 返 "not wired", client 隐藏播放按钮.
	briefing BriefingSynth

	// copilot — M9.4 RSS Co-Pilot 同步 Q&A. 两个依赖一起注入, 缺一即
	// copilot_ask 不可用.
	copilotBuilder CopilotContextBuilder
	copilotAsker   CopilotAsker

	// authz — M11.2 org-scope 授权器. nil 时任何 scope=org 操作 fail-closed
	// (ErrOrgScopeUnavailable). 接线在 app_center main.go (AUTHZ_URL).
	authz AuthzChecker

	// shareBaseURL — M11.3 公开分享链接的 base (APP_CENTER_BASE_URL). 空时
	// shares_create 仍返 token, 只是 url 字段为相对路径.
	shareBaseURL string
}

// EmbedQueryFn 计算单条文本的 embedding. 返 (vector, modelCode, err).
// modelCode 用于持久化到 watch_rules.semantic_embedding_model, 以便后续
// 模型切换时识别哪些规则需要重算.
type EmbedQueryFn func(ctx context.Context, text string) ([]float32, string, error)

// WithEmbedQuery 注入 query embedding 函数. 调用方 (app_center main.go)
// 通常包一下 embed.Worker 的 callOnce.
func (a *App) WithEmbedQuery(fn EmbedQueryFn) *App {
	a.embedQuery = fn
	return a
}

// WithBoards wires a rankings store. Optional; when set, the App
// exposes boards_list / boards_snapshot actions.
func (a *App) WithBoards(b BoardsStore) *App {
	a.boards = b
	return a
}

// WithRadar wires a radar store. Optional; when set, the App exposes
// rules_* / hits_* / radar_count actions and unread_count merges
// inbox + radar into a single severity-aware payload.
func (a *App) WithRadar(r RadarStore) *App {
	a.radar = r
	return a
}

// SchedulerRef exposes the internal refresh scheduler so external
// callers (app_center main wiring) can attach an OnNew callback for
// the radar matcher pipeline. Returns nil for in-memory mode.
func (a *App) SchedulerRef() *Scheduler { return a.sched }

// WithLLM wires an LLM advisor for natural-language → rule conversion
// (P3). Optional; when set the App exposes the rules_from_nl action.
func (a *App) WithLLM(l LLMAdvisor) *App {
	a.llm = l
	return a
}

func New() *App {
	return &App{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		store:      newStore(),
	}
}

// NewWithPool returns a PG-backed App. Subscriptions persist across
// restarts; entries / read state / etag-lastmod live in rss.* schema
// (see services/app_center/migrations/00006_rss_schema.sql).
func NewWithPool(pool *pgxpool.Pool) *App {
	pg := NewPGStore(pool)
	fetcher := NewDefaultFetcher()
	return &App{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		pg:         pg,
		sched:      NewScheduler(pg, fetcher),
		fetcher:    fetcher,
		discover:   NewDiscoverer(),
	}
}

func (a *App) Manifest() biuapp.Manifest {
	return biuapp.Manifest{
		Name:        Name,
		Version:     "0.2.0",
		Description: "Subscribe to RSS / Atom feeds; AI digest into wiki",
		Author:      "BiuMind",
		Permissions: []string{"net.outbound", "hub.invoke", "wiki.write", "cron.register"},
		Actions: []biuapp.ActionSpec{
			{
				Name:        "subscribe",
				Description: "Subscribe to an RSS / Atom feed URL.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"url"},
					"properties": map[string]any{
						"url":   map[string]any{"type": "string", "format": "uri", "title": "订阅地址"},
						"title": map[string]any{"type": "string", "title": "标题（可选）"},
					},
				},
			},
			{
				Name:        "unsubscribe",
				Description: "Remove a subscription by id.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
					},
				},
			},
			{
				Name:        "list_subscriptions",
				Description: "List the current user's RSS subscriptions.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "fetch",
				Description: "Fetch a feed URL and return up to `limit` items.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"url"},
					"properties": map[string]any{
						"url":   map[string]any{"type": "string"},
						"limit": map[string]any{"type": "integer", "default": 20},
					},
				},
			},
			{
				Name:        "refresh_all",
				Description: "Re-fetch every subscription; updates last_fetch.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "digest",
				Description: "Summarise recent items across subscriptions (LLM).",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"window":    map[string]any{"type": "string", "default": "24h"},
						"max_items": map[string]any{"type": "integer", "default": 10},
					},
				},
			},
			{
				Name:        "unread_count",
				Description: "Sum of unread item count across subscriptions; sidebar badge.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			// PG-backed actions (P0). When the App was constructed via
			// NewWithPool these resolve against rss.* schema; in-memory
			// instances return ErrNoCaller / not-wired errors.
			{
				Name:        "feeds_add",
				Description: "Add an RSS / Atom / JSON feed subscription (PG store).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"url"},
					"properties": map[string]any{
						"url":         map[string]any{"type": "string", "format": "uri"},
						"title":       map[string]any{"type": "string"},
						"category":    map[string]any{"type": "string"},
						"refresh_sec": map[string]any{"type": "integer", "minimum": 60},
					},
				},
			},
			{
				Name:        "feeds_list",
				Description: "List the caller's persisted feeds.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "feeds_remove",
				Description: "Remove a feed subscription (cascades entries).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string", "format": "uuid"},
					},
				},
			},
			{
				Name:        "feeds_refresh",
				Description: "Trigger an immediate refresh of all due feeds.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "entries_list",
				Description: "List entries for a feed (or across all caller feeds).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"feed_id":     map[string]any{"type": "string", "format": "uuid"},
						"unread_only": map[string]any{"type": "boolean"},
						"limit":       map[string]any{"type": "integer", "default": 50, "minimum": 1, "maximum": 500},
					},
				},
			},
			{
				Name:        "entries_mark_read",
				Description: "Mark an entry as read or unread.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id":   map[string]any{"type": "string", "format": "uuid"},
						"read": map[string]any{"type": "boolean", "default": true},
					},
				},
			},
			// T10.4.3 — cross-device reading resume (per-user scroll position).
			{
				Name:        "entry_progress_set",
				Description: "Save the per-user scroll position (0..1) for an entry, for cross-device resume.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"entry_id", "pct"},
					"properties": map[string]any{
						"entry_id": map[string]any{"type": "string", "format": "uuid"},
						"pct":      map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					},
				},
			},
			{
				Name:        "entry_progress_get",
				Description: "Get the saved per-user scroll position (0..1) for an entry. Returns pct=0 when none.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"entry_id"},
					"properties": map[string]any{
						"entry_id": map[string]any{"type": "string", "format": "uuid"},
					},
				},
			},
			// M6 — Discover / Saved / OPML.
			{
				Name:        "starter_packs_list",
				Description: "List curated feed bundles for one-click subscription.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "starter_packs_install",
				Description: "Subscribe to all feeds in a starter pack.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"pack_id"},
					"properties": map[string]any{
						"pack_id": map[string]any{"type": "string"},
					},
				},
			},
			{
				Name:        "marks_list",
				Description: "List entries marked star/pin/wiki/shared.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"mark":  map[string]any{"type": "string", "enum": []any{"star", "pin", "wiki", "shared"}},
						"limit": map[string]any{"type": "integer", "default": 100},
					},
				},
			},
			{
				Name:        "opml_export",
				Description: "Export all feeds as OPML 1.0 XML.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "export_archive",
				Description: "Export all RSS data (feeds/entries/marks/rules/prefs) as a base64 zip.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "opml_import",
				Description: "Bulk-add feeds from an OPML XML payload.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"xml"},
					"properties": map[string]any{
						"xml": map[string]any{"type": "string"},
					},
				},
			},
			// M5 — Source kind + feed URL discovery for any URL.
			{
				Name:        "feeds_discover",
				Description: "Detect source kind and resolve feed URL for any pasted URL.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"url"},
					"properties": map[string]any{
						"url": map[string]any{"type": "string"},
					},
				},
			},
			// M3 — Sink an entry into the user's BiuMind Wiki.
			{
				Name:        "entries_to_wiki",
				Description: "Save entry as a Wiki page (with AI takeaway + tags).",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string", "format": "uuid"},
					},
				},
			},
			// M2 — Today curated picks (headline + missed + trends + stats).
			{
				Name:        "today_picks",
				Description: "AI-curated daily picks for the Today view.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			// M11.3 — public read-only shared views.
			{
				Name:        "shares_create",
				Description: "Mint a public read-only share link for a view.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"view_kind"},
					"properties": map[string]any{
						"view_kind": map[string]any{
							"type": "string",
							"enum": []any{"today", "radar", "saved", "inbox"},
						},
						"filter":          map[string]any{"type": "object"},
						"expires_in_days": map[string]any{"type": "integer", "default": 30},
						"scope":           map[string]any{"type": "string", "enum": []any{"user", "org"}},
					},
				},
			},
			{
				Name:        "shares_list",
				Description: "List the caller's share links.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			// M11.4 — org admin forced subscriptions (Authz: rss:org_write).
			{
				Name:        "org_feeds_force_add",
				Description: "Force-subscribe a feed for all org members (admin).",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"feed_url"},
					"properties": map[string]any{
						"feed_url": map[string]any{"type": "string", "format": "uri"},
						"title":    map[string]any{"type": "string"},
					},
				},
			},
			{
				Name:        "org_feeds_force_remove",
				Description: "Remove a forced org subscription (admin).",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
					},
				},
			},
			// M11.5 — user preferences (Settings page).
			{
				Name:        "user_prefs_get",
				Description: "Get the caller's RSS preferences.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "user_prefs_update",
				Description: "Update the caller's RSS preferences (whole config object).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"config"},
					"properties": map[string]any{
						"config": map[string]any{"type": "object"},
					},
				},
			},
			{
				Name:        "shares_revoke",
				Description: "Revoke a share link by token.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"token"},
					"properties": map[string]any{
						"token": map[string]any{"type": "string"},
					},
				},
			},
			// M8.4 — Today 简报朗读 (cosyvoice TTS, 24h cache).
			// 返 base64 编码的 mp3 + 元数据 (script/voice/cached);
			// client 直接 AudioPlayer 播放.
			{
				Name:        "briefing_today_audio",
				Description: "Today 简报 mp3 (TTS), 24h 缓存. 返 audio_b64 base64.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			// M9.4 — RSS Co-Pilot 同步 Q&A. 客户端右抽屉一问一答, 后端
			// 注入当前 view 的 entries 做 LLM context, 返 markdown 答案
			// + 引用 chip (citations[].n/entry_id 用于跳转 reader).
			{
				Name:        "copilot_ask",
				Description: "Ask the RSS AI Co-Pilot. Returns answer + citations.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"question"},
					"properties": map[string]any{
						"view_kind": map[string]any{
							"type": "string",
							"enum": []any{"today", "radar", "inbox", "reader"},
						},
						"question":         map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
						"current_entry_id": map[string]any{"type": "string", "format": "uuid"},
					},
				},
			},
			// M9 — 雷达规则的 action 执行历史. 供 RuleEditor 卡片下方
			// "执行历史" 折叠区显示 (rule_id, limit) → action_runs[].
			{
				Name:        "action_runs_list",
				Description: "List recent action_runs for a rule (M9 dispatcher 写入).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"rule_id"},
					"properties": map[string]any{
						"rule_id": map[string]any{"type": "string", "format": "uuid"},
						"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
					},
				},
			},
			// M1 — Persona-based article rephrase.
			{
				Name:        "entries_rephrase",
				Description: "Rewrite an entry in a target persona voice (5-200 chars).",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id", "persona"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string", "format": "uuid"},
						"persona": map[string]any{
							"type": "string",
							"enum": []any{"child", "layman", "boss", "expert", "english"},
						},
					},
				},
			},
			// P3 — Natural-language → rule via LLM.
			{
				Name:        "rules_from_nl",
				Description: "Convert a one-sentence description into a rule draft via LLM.",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"text"},
					"properties": map[string]any{
						"text": map[string]any{
							"type":      "string",
							"minLength": 4,
							"maxLength": 500,
							"title":     "用一句话描述你想监控的事",
						},
					},
				},
			},
			// Radar (P2) — keyword rules + hits.
			{
				Name:        "rules_create",
				Description: "Create a keyword watch rule.",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"name"},
					"properties": map[string]any{
						"name":          map[string]any{"type": "string"},
						"match_any":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"match_all":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"exclude":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"sources":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "default": []any{"*"}},
						"on_hit_badge":  map[string]any{"type": "string", "enum": []any{"info", "warn", "error"}, "default": "warn"},
						"on_hit_notify": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"cooldown_sec":  map[string]any{"type": "integer", "default": 1800, "minimum": 0},
					},
				},
			},
			{
				Name:        "rules_list",
				Description: "List the caller's watch rules.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "rules_update",
				Description: "Update a watch rule.",
				Risk:        biuapp.RiskMedium,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id":           map[string]any{"type": "string", "format": "uuid"},
						"name":         map[string]any{"type": "string"},
						"enabled":      map[string]any{"type": "boolean"},
						"on_hit_badge": map[string]any{"type": "string", "enum": []any{"info", "warn", "error"}},
						"cooldown_sec": map[string]any{"type": "integer"},
					},
				},
			},
			{
				Name:        "rules_delete",
				Description: "Delete a watch rule (cascades hits).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string", "format": "uuid"},
					},
				},
			},
			{
				Name:        "hits_list",
				Description: "List radar hits (cross-rule by default).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"rule_id":     map[string]any{"type": "string", "format": "uuid"},
						"unread_only": map[string]any{"type": "boolean"},
						"limit":       map[string]any{"type": "integer", "default": 100, "minimum": 1, "maximum": 500},
					},
				},
			},
			{
				Name:        "hits_mark_read",
				Description: "Mark a hit acknowledged.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id"},
					"properties": map[string]any{
						"id": map[string]any{"type": "integer"},
					},
				},
			},
			{
				Name:        "radar_count",
				Description: "Unread radar hit count + max severity (badge action).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			// Rankings (P1) — read-only access to global hot lists.
			{
				Name:        "boards_list",
				Description: "List configured rankings boards (newsnow-backed).",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "boards_snapshot",
				Description: "Latest snapshot of a board with rank deltas + 新进榜 flags.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"board_id"},
					"properties": map[string]any{
						"board_id": map[string]any{"type": "string"},
						"limit":    map[string]any{"type": "integer", "default": 30, "minimum": 1, "maximum": 100},
					},
				},
			},
			{
				Name:        "entries_star",
				Description: "Star or unstar an entry.",
				Risk:        biuapp.RiskLow,
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"id", "starred"},
					"properties": map[string]any{
						"id":      map[string]any{"type": "string", "format": "uuid"},
						"starred": map[string]any{"type": "boolean"},
					},
				},
			},
		},
		ManifestExt: biuapp.ManifestExt{
			Identifier: Name,
			Title:      "RSS 订阅",
			Category:   "content",
			Kind:       "hybrid",

			// Views — AppViewHost (M7) renders these.
			Views: []biuapp.ViewSpec{
				// M2 — Today curated front page (default landing view).
				// Layout=custom — the in-app TodayTab widget owns
				// the rendering; AppViewHost short-circuits this app
				// already so layout isn't actually consulted.
				{
					ID:     "today",
					Route:  "/apps/rss/today",
					Title:  "Today",
					Layout: biuapp.LayoutCustom,
				},
				{
					ID:         "home",
					Route:      "/apps/rss",
					Title:      "RSS 订阅",
					Layout:     biuapp.LayoutListDetail,
					DataSource: &biuapp.ViewDataSource{Action: "list_subscriptions"},
					RefreshOn: []string{
						"app:install:<self>:subscription_added",
						"app:install:<self>:subscription_removed",
					},
					ItemTemplate: &biuapp.ViewItemTemplate{
						Kind:     "card",
						Title:    "${item.title}",
						Subtitle: "${item.unread} 未读 · ${item.last_fetch | relative_time | default(从未)}",
						Body:     "${item.url}",
						Actions: []biuapp.ViewActionRef{
							{
								Label:   "取消订阅",
								Icon:    "trash",
								Action:  "unsubscribe",
								Input:   map[string]any{"id": "${item.id}"},
								Confirm: "确认取消订阅 ${item.title}？",
								OnSuccess: &biuapp.ViewActionEffect{
									Toast:   "已取消订阅",
									Refresh: true,
								},
							},
						},
					},
					Toolbar: []biuapp.ViewActionRef{
						{Label: "添加订阅", Icon: "add", Route: "/apps/rss/add"},
						{Label: "立即刷新", Icon: "refresh", Action: "refresh_all",
							OnSuccess: &biuapp.ViewActionEffect{Toast: "已刷新", Refresh: true}},
						{Label: "雷达", Icon: "radar", Route: "/apps/rss/radar"},
						{Label: "榜单", Icon: "trending_up", Route: "/apps/rss/boards"},
					},
				},
				{
					ID:        "add",
					Route:     "/apps/rss/add",
					Title:     "添加订阅",
					Layout:    biuapp.LayoutForm,
					SchemaRef: "actions.subscribe.input_schema",
					Submit: &biuapp.FormSubmit{
						Action: "subscribe",
						OnSuccess: &biuapp.ViewActionEffect{
							Toast:    "订阅成功",
							Navigate: "/apps/rss",
						},
					},
				},
				// P1 — Boards (中文热榜) tab. Grid of board cards;
				// clicking opens a detail page with top-N items.
				{
					ID:         "boards",
					Route:      "/apps/rss/boards",
					Title:      "榜单",
					Layout:     biuapp.LayoutGrid,
					DataSource: &biuapp.ViewDataSource{Action: "boards_list"},
					ItemTemplate: &biuapp.ViewItemTemplate{
						Kind:     "card",
						Title:    "${item.name}",
						Subtitle: "${item.last_fetched_at | relative_time | default(从未刷新)}",
						Body:     "状态: ${item.last_status | default(等待首次抓取)}",
						Actions: []biuapp.ViewActionRef{
							{
								Label: "查看 top 30",
								Icon:  "chevron_right",
								Route: "/apps/rss/boards/${item.id}",
							},
						},
					},
					Toolbar: []biuapp.ViewActionRef{
						{Label: "我的订阅", Icon: "rss_feed", Route: "/apps/rss"},
					},
				},
				// P2 — Radar tab. Shows hit timeline + entry to rule
				// management.
				{
					ID:         "radar",
					Route:      "/apps/rss/radar",
					Title:      "雷达",
					Layout:     biuapp.LayoutListDetail,
					DataSource: &biuapp.ViewDataSource{Action: "hits_list"},
					ItemTemplate: &biuapp.ViewItemTemplate{
						Kind:     "card",
						Title:    "${item.title}",
						Subtitle: "${item.severity_label} ${item.rule_name} · ${item.source} · ${item.hit_at | relative_time}",
						Body:     "${item.url | domain}",
						Actions: []biuapp.ViewActionRef{
							{
								Label:  "标记已读",
								Icon:   "check",
								Action: "hits_mark_read",
								Input:  map[string]any{"id": "${item.id}"},
								OnSuccess: &biuapp.ViewActionEffect{
									Toast: "已读", Refresh: true,
								},
							},
						},
					},
					Toolbar: []biuapp.ViewActionRef{
						{Label: "管理规则", Icon: "settings", Route: "/apps/rss/rules"},
						{Label: "我的订阅", Icon: "rss_feed", Route: "/apps/rss"},
					},
				},
				{
					ID:         "rules",
					Route:      "/apps/rss/rules",
					Title:      "雷达规则",
					Layout:     biuapp.LayoutListDetail,
					DataSource: &biuapp.ViewDataSource{Action: "rules_list"},
					ItemTemplate: &biuapp.ViewItemTemplate{
						Kind:     "card",
						Title:    "${item.name}",
						Subtitle: "命中级别: ${item.on_hit_badge} · 冷却: ${item.cooldown_sec}s",
						Body:     "${item.match_any} ${item.match_all}",
						Actions: []biuapp.ViewActionRef{
							{
								Label:   "删除",
								Icon:    "trash",
								Action:  "rules_delete",
								Input:   map[string]any{"id": "${item.id}"},
								Confirm: "确认删除规则 ${item.name}？",
								OnSuccess: &biuapp.ViewActionEffect{
									Toast: "已删除", Refresh: true,
								},
							},
						},
					},
					Toolbar: []biuapp.ViewActionRef{
						{Label: "新建规则", Icon: "add", Route: "/apps/rss/rules/add"},
						{Label: "返回雷达", Icon: "arrow_back", Route: "/apps/rss/radar"},
					},
				},
				{
					ID:        "rule_add",
					Route:     "/apps/rss/rules/add",
					Title:     "新建雷达规则",
					Layout:    biuapp.LayoutForm,
					SchemaRef: "actions.rules_create.input_schema",
					Submit: &biuapp.FormSubmit{
						Action: "rules_create",
						OnSuccess: &biuapp.ViewActionEffect{
							Toast:    "规则已创建",
							Navigate: "/apps/rss/rules",
						},
					},
				},
				{
					ID:     "board_detail",
					Route:  "/apps/rss/boards/:board_id",
					Title:  "榜单详情",
					Layout: biuapp.LayoutListDetail,
					DataSource: &biuapp.ViewDataSource{
						Action: "boards_snapshot",
						Input: map[string]any{
							"board_id": "${route.board_id}",
							"limit":    30,
						},
					},
					ItemTemplate: &biuapp.ViewItemTemplate{
						Kind:     "card",
						Title:    "${item.rank_label} ${item.title}",
						Subtitle: "${item.new_label} ${item.delta_label} ${item.info | default()}",
						Body:     "${item.url | domain}",
					},
				},
			},

			Triggers: []biuapp.TriggerSpec{
				{
					Kind:   biuapp.TriggerCron,
					Name:   "hourly_refresh",
					Expr:   "5 * * * *",
					Action: "refresh_all",
				},
				{
					Kind:   biuapp.TriggerCron,
					Name:   "daily_digest",
					Expr:   "0 8 * * *",
					Action: "digest",
					Input:  map[string]any{"window": "24h", "max_items": 10},
				},
			},

			Skills: []biuapp.SkillRef{
				{Identifier: "rss-summarize", File: "skills/summarize.md"},
			},

			Sidebar: &biuapp.SidebarHints{
				PreferredPosition:    "middle",
				MobileBottomEligible: true,
				BadgeAction:          "unread_count",
				BadgeRefreshSec:      120,
			},
		},
	}
}

func (a *App) Init(ctx context.Context, deps biuapp.Deps) error { return nil }

// SkillContent returns the embedded SKILL.md body for the named
// bundled skill. Implements biuapp.BundledSkillProvider so the App
// Center install path can write skill content to runtime.skills
// without filesystem access at install time.
func (a *App) SkillContent(identifier string) ([]byte, error) {
	switch identifier {
	case "rss-summarize":
		return summarizeSkill, nil
	}
	return nil, biuapp.ErrSkillNotFound
}

type fetchInput struct {
	URL   string `json:"url"`
	Limit int    `json:"limit"`
}

type Item struct {
	Title     string    `json:"title"`
	Link      string    `json:"link,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Published time.Time `json:"published,omitempty"`
	GUID      string    `json:"guid,omitempty"`
}

type Output struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	ItemCount int    `json:"item_count"`
	Items     []Item `json:"items"`
}

func (a *App) Invoke(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	res, err := a.invokeInternal(ctx, action, raw)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	RecordAction(action, outcome)
	return res, err
}

func (a *App) invokeInternal(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	// PG-backed actions (P0+).
	if a.pg != nil {
		switch action {
		case "feeds_add":
			return a.invokeFeedsAdd(ctx, raw)
		case "feeds_list":
			return a.invokeFeedsList(ctx, raw)
		case "feeds_remove":
			return a.invokeFeedsRemove(ctx, raw)
		case "feeds_refresh":
			return a.invokeFeedsRefresh(ctx, raw)
		case "entries_list":
			return a.invokeEntriesList(ctx, raw)
		case "entries_mark_read":
			return a.invokeEntriesMarkRead(ctx, raw)
		case "entries_star":
			return a.invokeEntriesStar(ctx, raw)
		case "entry_progress_set":
			return a.invokeEntryProgressSet(ctx, raw)
		case "entry_progress_get":
			return a.invokeEntryProgressGet(ctx, raw)
		case "boards_list":
			return a.invokeBoardsList(ctx, raw)
		case "boards_snapshot":
			return a.invokeBoardsSnapshot(ctx, raw)
		case "rules_create":
			return a.invokeRulesCreate(ctx, raw)
		case "rules_list":
			return a.invokeRulesList(ctx, raw)
		case "rules_update":
			return a.invokeRulesUpdate(ctx, raw)
		case "rules_delete":
			return a.invokeRulesDelete(ctx, raw)
		case "hits_list":
			return a.invokeHitsList(ctx, raw)
		case "hits_mark_read":
			return a.invokeHitsMarkRead(ctx, raw)
		case "radar_count":
			return a.invokeRadarCount(ctx, raw)
		case "rules_from_nl":
			return a.invokeRulesFromNL(ctx, raw)
		case "entries_rephrase":
			return a.invokeEntriesRephrase(ctx, raw)
		case "briefing_today_audio":
			return a.invokeBriefingTodayAudio(ctx, raw)
		case "action_runs_list":
			return a.invokeActionRunsList(ctx, raw)
		case "copilot_ask":
			return a.invokeCopilotAsk(ctx, raw)
		case "today_picks":
			return a.invokeTodayPicks(ctx, raw)
		case "entries_to_wiki":
			return a.invokeEntriesToWiki(ctx, raw)
		case "feeds_discover":
			return a.invokeFeedsDiscover(ctx, raw)
		case "starter_packs_list":
			return a.invokeStarterPacksList(ctx, raw)
		case "starter_packs_install":
			return a.invokeStarterPacksInstall(ctx, raw)
		case "marks_list":
			return a.invokeMarksList(ctx, raw)
		case "shares_create":
			return a.invokeSharesCreate(ctx, raw)
		case "shares_list":
			return a.invokeSharesList(ctx, raw)
		case "shares_revoke":
			return a.invokeSharesRevoke(ctx, raw)
		case "org_feeds_force_add":
			return a.invokeOrgFeedsForceAdd(ctx, raw)
		case "org_feeds_force_remove":
			return a.invokeOrgFeedsForceRemove(ctx, raw)
		case "user_prefs_get":
			return a.invokeUserPrefsGet(ctx, raw)
		case "user_prefs_update":
			return a.invokeUserPrefsUpdate(ctx, raw)
		case "export_archive":
			return a.invokeExportArchive(ctx, raw)
		case "opml_export":
			return a.invokeOpmlExport(ctx, raw)
		case "opml_import":
			return a.invokeOpmlImport(ctx, raw)
		case "unread_count":
			return a.invokeUnreadCountPG(ctx)
		// Old action names delegate to PG when wired so existing UI
		// callers keep working through the migration.
		case "subscribe":
			return a.invokeFeedsAdd(ctx, raw)
		case "unsubscribe":
			return a.invokeFeedsRemove(ctx, raw)
		case "list_subscriptions":
			return a.invokeFeedsList(ctx, raw)
		case "refresh_all":
			return a.invokeFeedsRefresh(ctx, raw)
		}
		// In PG-backed mode any action not matched above is a legacy
		// in-memory-only action (digest / fetch) whose handlers dereference
		// a.store — which is nil when pg is wired. Fail loud rather than
		// nil-panic: a stale cron trigger firing "digest" previously
		// SIGSEGV'd the whole app_center process on every tick.
		return nil, fmt.Errorf("rss: action %q not supported in PG-backed mode", action)
	}
	switch action {
	case "fetch":
		return a.invokeFetch(ctx, raw)
	case "subscribe":
		return a.invokeSubscribe(ctx, raw)
	case "unsubscribe":
		return a.invokeUnsubscribe(ctx, raw)
	case "list_subscriptions":
		return a.invokeListSubscriptions(ctx)
	case "refresh_all":
		return a.invokeRefreshAll(ctx)
	case "digest":
		return a.invokeDigest(ctx, raw)
	case "unread_count":
		return a.invokeUnreadCount()
	}
	return nil, fmt.Errorf("rss: %w: %s", biuapp.ErrUnknownAction, action)
}

// invokeUnreadCount 给 sidebar badge 用 (设计 §10A.9): 累加所有订阅的
// Unread (= 上次 refresh 拉到的 item 数, 真正的"用户未读"待 v2.0 加
// per-item read 状态)。severity 按规模分档: ≥50 warn, 否则 info。
func (a *App) invokeUnreadCount() (any, error) {
	total := 0
	for _, s := range a.store.List() {
		total += s.Unread
	}
	severity := "info"
	if total >= 50 {
		severity = "warn"
	}
	return map[string]any{
		"count":    total,
		"severity": severity,
	}, nil
}

func (a *App) invokeFetch(ctx context.Context, raw json.RawMessage) (any, error) {
	var in fetchInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.URL == "" {
		return nil, errors.New("rss: missing url")
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 20
	}
	return a.fetch(ctx, in)
}

type subscribeInput struct {
	URL   string   `json:"url"`
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
}

func (a *App) invokeSubscribe(ctx context.Context, raw json.RawMessage) (any, error) {
	var in subscribeInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.URL == "" {
		return nil, errors.New("rss: subscribe requires url")
	}
	// Title defaults: best-effort fetch the feed to pull <title>;
	// fail gracefully because users often subscribe to broken URLs
	// they want to fix later (the App still records the row so
	// they can edit/remove from the UI).
	title := in.Title
	if title == "" {
		out, err := a.fetch(ctx, fetchInput{URL: in.URL, Limit: 1})
		if err == nil && out.Title != "" {
			title = out.Title
		} else {
			title = in.URL
		}
	}
	sub := a.store.Add(in.URL, title, in.Tags)
	return map[string]any{
		"subscription_id": sub.ID,
		"title":           sub.Title,
		"url":             sub.URL,
	}, nil
}

func (a *App) invokeUnsubscribe(_ context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.ID == "" {
		return nil, errors.New("rss: unsubscribe requires id")
	}
	if !a.store.Remove(in.ID) {
		return nil, fmt.Errorf("rss: subscription %q not found", in.ID)
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) invokeListSubscriptions(_ context.Context) (any, error) {
	subs := a.store.List()
	// AppViewHost expects {items: [...]} for list / list_detail layouts.
	items := make([]map[string]any, len(subs))
	for i, s := range subs {
		items[i] = map[string]any{
			"id":         s.ID,
			"url":        s.URL,
			"title":      s.Title,
			"tags":       s.Tags,
			"unread":     s.Unread,
			"added_at":   s.AddedAt,
			"last_fetch": s.LastFetch,
		}
	}
	return map[string]any{"items": items}, nil
}

// invokeRefreshAll re-fetches every subscription. Errors per-feed
// don't abort the loop — we collect them and return a count so the
// scheduler can report partial success without crashing the run.
func (a *App) invokeRefreshAll(ctx context.Context) (any, error) {
	subs := a.store.List()
	refreshed := 0
	failed := 0
	for _, s := range subs {
		out, err := a.fetch(ctx, fetchInput{URL: s.URL, Limit: 50})
		if err != nil {
			failed++
			continue
		}
		// Unread is a placeholder — real unread tracking needs a
		// last_seen marker per item, which lands when we move
		// storage into Brain.Wiki (v2.0). For now: report the
		// number of items returned, which is at least proportional
		// to "fresh content".
		a.store.MarkFetched(s.ID, len(out.Items))
		refreshed++
	}
	return map[string]any{
		"refreshed": refreshed,
		"failed":    failed,
		"total":     len(subs),
	}, nil
}

type digestInput struct {
	Window   string `json:"window"`
	MaxItems int    `json:"max_items"`
}

// invokeDigest is a thin coordinator — it pulls items but does NOT
// call the LLM directly (App SDK can't hold model-relay creds; permission
// hub.invoke routes via Deps.model-relay which v1.5 doesn't yet inject for
// bundled apps). Callers (the Agent loop or the cron dispatcher)
// receive the assembled payload and feed it to the model itself.
//
// Returning structured data instead of a finished string is also the
// shape rss-summarize skill expects — the skill's prompt walks the
// model through the summarisation, the App just provides the raw
// material.
func (a *App) invokeDigest(ctx context.Context, raw json.RawMessage) (any, error) {
	var in digestInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.MaxItems <= 0 {
		in.MaxItems = 10
	}
	dur, err := parseWindow(in.Window)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-dur)
	subs := a.store.List()
	var collected []Item
	sourceCount := 0
	for _, s := range subs {
		out, err := a.fetch(ctx, fetchInput{URL: s.URL, Limit: 50})
		if err != nil {
			continue
		}
		sourceCount++
		for _, it := range out.Items {
			if !it.Published.IsZero() && it.Published.Before(cutoff) {
				continue
			}
			collected = append(collected, it)
			if len(collected) >= in.MaxItems {
				break
			}
		}
		if len(collected) >= in.MaxItems {
			break
		}
	}
	return map[string]any{
		"items":        collected,
		"source_count": sourceCount,
		"window":       in.Window,
		"cutoff":       cutoff,
	}, nil
}

func parseWindow(s string) (time.Duration, error) {
	if s == "" {
		return 24 * time.Hour, nil
	}
	// Accept "24h" / "7d" / "30m"; time.ParseDuration accepts h/m/s.
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("rss: bad window %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("rss: bad window %q: %w", s, err)
	}
	return d, nil
}

func (a *App) fetch(ctx context.Context, in fetchInput) (*Output, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "biumind-rss/0.1")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rss: %s -> %d", in.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5MB cap
	if err != nil {
		return nil, err
	}
	return ParseFeed(body, in.URL, in.Limit)
}

// ParseFeed is exported for tests + future "ingest worker" reuse.
func ParseFeed(body []byte, url string, limit int) (*Output, error) {
	// Try RSS first (covers RSS 2.0 + 1.0 with namespaces).
	var rssDoc struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Title string `xml:"title"`
			Item  []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
				PubDate     string `xml:"pubDate"`
				GUID        string `xml:"guid"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rssDoc); err == nil && rssDoc.XMLName.Local == "rss" {
		items := make([]Item, 0, len(rssDoc.Channel.Item))
		for _, e := range rssDoc.Channel.Item {
			items = append(items, Item{
				Title:     strings.TrimSpace(e.Title),
				Link:      strings.TrimSpace(e.Link),
				Summary:   strings.TrimSpace(e.Description),
				Published: parseTime(e.PubDate),
				GUID:      strings.TrimSpace(e.GUID),
			})
			if len(items) >= limit {
				break
			}
		}
		return &Output{Title: rssDoc.Channel.Title, URL: url, ItemCount: len(items), Items: items}, nil
	}

	// Fallback: Atom.
	var atomDoc struct {
		XMLName xml.Name `xml:"feed"`
		Title   string   `xml:"title"`
		Entry   []struct {
			Title string `xml:"title"`
			Link  []struct {
				Href string `xml:"href,attr"`
				Rel  string `xml:"rel,attr"`
			} `xml:"link"`
			Summary string `xml:"summary"`
			Content string `xml:"content"`
			Updated string `xml:"updated"`
			ID      string `xml:"id"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &atomDoc); err == nil && atomDoc.XMLName.Local == "feed" {
		items := make([]Item, 0, len(atomDoc.Entry))
		for _, e := range atomDoc.Entry {
			link := ""
			for _, l := range e.Link {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			summary := e.Summary
			if summary == "" {
				summary = e.Content
			}
			items = append(items, Item{
				Title:     strings.TrimSpace(e.Title),
				Link:      strings.TrimSpace(link),
				Summary:   strings.TrimSpace(summary),
				Published: parseTime(e.Updated),
				GUID:      strings.TrimSpace(e.ID),
			})
			if len(items) >= limit {
				break
			}
		}
		return &Output{Title: atomDoc.Title, URL: url, ItemCount: len(items), Items: items}, nil
	}
	return nil, errors.New("rss: not RSS or Atom")
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC1123Z, time.RFC1123, time.RFC3339,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
