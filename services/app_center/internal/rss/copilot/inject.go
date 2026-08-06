// Package copilot — M9.4 RSS Co-Pilot: 把当前 view (today / radar / inbox /
// reader) 的相关条目注入到 LLM 提示, 一问一答返带 [N] 引用的答案.
//
// 设计取舍:
//   - 同步 Q&A (一次 model-relay /v1/messages 一次性调用), 不走 brain
//     agent session. 抽屉式轻量交互, thread/history/tool-use 都不需要.
//   - 引用通过 system_prompt 里的 "Items: [1] ... [2] ..." 强制约束 LLM
//     输出 [N] 标记 — 不依赖 tool calling. 后处理用正则提取 [N] tokens.
//   - context 上限 ~3000 字 (中文) / 4k tokens, 留空间给答案. 超出则
//     按 ai_importance desc + recency 截断.
//
// view_kind 枚举:
//   today  — pull TodayPicks.headline (top 5 已是 AI 排序)
//   radar  — 该 user 最近 24h 的 watch_hits + 关联 rule
//   inbox  — 用户当前可见 entries (筛选 feed_id / 关键词留 future)
//   reader — current_entry_id 必填; 拉这一条 entry 全文做单文档 Q&A

package copilot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxItems       = 8     // [1]..[8] 上限, 多了 LLM 容易混
	maxContextChar = 3000  // 中文字符约 4500 tokens
	maxItemChar    = 280   // 单条目 takeaway 最多 280 字
)

// Item — 注入 prompt 的一条 entry, 也作为引用 mapping 返客户端.
type Item struct {
	N       int    // 1-based, 用作 [N] 引用标号
	EntryID string // 客户端用来跳转 reader
	Title   string
	URL     string
	Source  string // feed title / 'rss:<feed_id>' / 'boards:xxx'
	Summary string // ai_takeaway 截断后的版本
}

// Build — 给定 view_kind + ctx 数据, 返 (system_prompt, items, error).
// system_prompt 已经把 items 编号好嵌进去, caller 直接拿去当 system 用.
//
// scope 限制: items 必须属于 userID 自己的 feeds (跨用户保护).
func Build(
	ctx context.Context, pool *pgxpool.Pool,
	userID, viewKind, currentEntryID string,
) (systemPrompt string, items []Item, err error) {
	if pool == nil {
		return "", nil, fmt.Errorf("copilot: nil pool")
	}
	switch viewKind {
	case "today":
		items, err = pickToday(ctx, pool, userID)
	case "radar":
		items, err = pickRadar(ctx, pool, userID)
	case "inbox":
		items, err = pickInbox(ctx, pool, userID)
	case "reader":
		if currentEntryID == "" {
			return "", nil, fmt.Errorf("copilot: reader view requires current_entry_id")
		}
		items, err = pickReader(ctx, pool, userID, currentEntryID)
	default:
		return "", nil, fmt.Errorf("copilot: unknown view_kind %q", viewKind)
	}
	if err != nil {
		return "", nil, err
	}
	systemPrompt = renderPrompt(viewKind, items)
	return systemPrompt, items, nil
}

func renderPrompt(viewKind string, items []Item) string {
	var sb strings.Builder
	sb.WriteString("你是 BiuMind RSS 阅读器的 AI 助手, 帮用户理解 / 检索 / 总结他订阅的内容. ")
	sb.WriteString("基于下方条目 (Items 1..N) 回答, 不要编造未列出的事实. ")
	sb.WriteString("引用条目时必须用 [N] 数字标记 (例如 [1] [2]) — 客户端会把它解析成可点击链接, ")
	sb.WriteString("不要拼成 [link][1] 也不要写 ([1]).\n\n")

	switch viewKind {
	case "today":
		sb.WriteString("当前视图: Today (AI 已挑出今天最值得看的几条).\n")
	case "radar":
		sb.WriteString("当前视图: 雷达命中 (用户配的规则最近触发的条目).\n")
	case "inbox":
		sb.WriteString("当前视图: 收件箱 (订阅源最近的更新).\n")
	case "reader":
		sb.WriteString("当前视图: 单篇阅读 (用户正在看 [1] 这一条, 围绕它问).\n")
	}

	if len(items) == 0 {
		sb.WriteString("\n(暂无相关条目, 直接告诉用户没有数据可用.)\n")
		return sb.String()
	}

	sb.WriteString("\nItems:\n")
	for _, it := range items {
		fmt.Fprintf(&sb, "[%d] %s", it.N, it.Title)
		if it.Source != "" {
			fmt.Fprintf(&sb, " (来源: %s)", it.Source)
		}
		sb.WriteByte('\n')
		if it.Summary != "" {
			fmt.Fprintf(&sb, "    %s\n", it.Summary)
		}
	}
	return sb.String()
}

// pickToday — 选 today 视图的 top 5 headline (AI 已排序).
func pickToday(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Item, error) {
	// 复用 today picker 已有的 SQL 模式: scope=user 的 entries + ai_importance
	// desc + 最近 24h. 不直接 import today 包避免环依.
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.title, e.url, COALESCE(f.title, ''),
		       COALESCE(NULLIF(e.ai_takeaway, ''), '')
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE f.scope = 'user' AND f.scope_id = $1
		   AND e.read_at IS NULL
		   AND e.fetched_at > now() - interval '24 hours'
		 ORDER BY e.ai_importance DESC NULLS LAST, e.fetched_at DESC
		 LIMIT $2`, userID, maxItems)
	if err != nil {
		return nil, fmt.Errorf("copilot: today query: %w", err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// pickRadar — 该 user 最近 24h 的 watch_hits.
func pickRadar(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Item, error) {
	rows, err := pool.Query(ctx, `
		SELECT h.id::text, h.title, h.url, COALESCE(r.name, '') AS source,
		       ''
		  FROM rss.watch_hits h
		  JOIN rss.watch_rules r ON r.id = h.rule_id
		 WHERE r.scope = 'user' AND r.scope_id = $1
		   AND h.hit_at > now() - interval '24 hours'
		 ORDER BY h.hit_at DESC
		 LIMIT $2`, userID, maxItems)
	if err != nil {
		return nil, fmt.Errorf("copilot: radar query: %w", err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// pickInbox — 最近的 entries 按 fetched_at desc.
func pickInbox(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Item, error) {
	rows, err := pool.Query(ctx, `
		SELECT e.id, e.title, e.url, COALESCE(f.title, ''),
		       COALESCE(NULLIF(e.ai_takeaway, ''), '')
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE f.scope = 'user' AND f.scope_id = $1
		 ORDER BY e.fetched_at DESC
		 LIMIT $2`, userID, maxItems)
	if err != nil {
		return nil, fmt.Errorf("copilot: inbox query: %w", err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// pickReader — 单篇 + 全文 (content_text 截断). 跨 scope 保护.
func pickReader(ctx context.Context, pool *pgxpool.Pool, userID, entryID string) ([]Item, error) {
	id, err := uuid.Parse(entryID)
	if err != nil {
		return nil, fmt.Errorf("copilot: bad entry id: %w", err)
	}
	var (
		title, url, feedTitle, takeaway, contentText string
	)
	err = pool.QueryRow(ctx, `
		SELECT e.title, e.url, COALESCE(f.title, ''),
		       COALESCE(NULLIF(e.ai_takeaway, ''), ''),
		       COALESCE(NULLIF(e.content_text, ''), '')
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE e.id = $1
		   AND f.scope = 'user' AND f.scope_id = $2`, id, userID).
		Scan(&title, &url, &feedTitle, &takeaway, &contentText)
	if err != nil {
		return nil, fmt.Errorf("copilot: reader query: %w", err)
	}
	body := takeaway
	if body == "" {
		body = contentText
	}
	return []Item{{
		N:       1,
		EntryID: entryID,
		Title:   title,
		URL:     url,
		Source:  feedTitle,
		Summary: clip(body, maxItemChar*4), // reader view 给 4x 长度
	}}, nil
}

// rowsScanner — 同 pgx.Rows 子集, 避免 import pgx.Rows 类型本身.
type rowsScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanItems(rows rowsScanner) ([]Item, error) {
	out := make([]Item, 0, maxItems)
	totalChar := 0
	for rows.Next() {
		var id, title, url, source, summary string
		if err := rows.Scan(&id, &title, &url, &source, &summary); err != nil {
			return nil, err
		}
		summary = clip(summary, maxItemChar)
		// context budget — 满了就停 (单条 ~ title + summary 估算)
		add := utf8Len(title) + utf8Len(summary) + 50 /* 标号 + 来源开销 */
		if totalChar+add > maxContextChar && len(out) > 0 {
			break
		}
		totalChar += add
		out = append(out, Item{
			N:       len(out) + 1,
			EntryID: id,
			Title:   title,
			URL:     url,
			Source:  source,
			Summary: summary,
		})
	}
	return out, rows.Err()
}

func clip(s string, max int) string {
	if utf8Len(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

func utf8Len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// (kept for future: time-based filter / topic boost — not in v3 M9.4)
var _ = time.Now
