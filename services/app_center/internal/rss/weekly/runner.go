// Package weekly — M9.5 上周回顾 cron.
//
// 触发: 每周日 08:00 UTC 之后第一次 5min tick. 通过 weekly_runs 主键
// 幂等 (同 user_id × iso_week 只允许一行), 重启 / 多副本不会重复写.
//
// 每用户流程:
//   1. 拉上周 7d 数据: starred 个数 / read 个数 / wiki 沉淀个数 / ai_topics
//      聚类 (取 top 3)
//   2. 拉 5 篇最重要的 entries (按 ai_importance desc + starred=true 加权)
//   3. LLM (sonnet 4.6 fallback glm-5.1) 用 system prompt 写 "上周回顾"
//      markdown
//   4. brain.Wiki POST 到 "信息流/周报/<iso_week>" 页面
//   5. INSERT weekly_runs (success / 失败都记, 失败带 error)

package weekly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultTickInterval = 5 * time.Minute
	defaultModel        = "glm-5.1" // sonnet 4.6 上线后切
	maxTopEntries       = 5
	scanWindow          = 7 * 24 * time.Hour
)

type Runner struct {
	Pool          *pgxpool.Pool
	BrainURL      string
	ModelRelayURL string
	Model         string
	Logger        *slog.Logger
	HTTP          *http.Client

	// SignFor — 同 digest/embed/copilot worker 模式: 签 per-user JWT,
	// 让 LLM/wiki 调用计在用户名下.
	SignFor func(userID string) (string, error)
}

func New(pool *pgxpool.Pool, brainURL, modelRelayURL string) *Runner {
	return &Runner{
		Pool:          pool,
		BrainURL:      strings.TrimRight(brainURL, "/"),
		ModelRelayURL: strings.TrimRight(modelRelayURL, "/"),
		Model:         defaultModel,
		Logger:        slog.Default(),
		HTTP:          &http.Client{Timeout: 60 * time.Second},
	}
}

// Start 启动 5min tick. 失败只记日志不退出 — 下一 tick 重试.
func (r *Runner) Start(ctx context.Context) {
	if r.Pool == nil || r.ModelRelayURL == "" {
		r.Logger.Info("weekly: skip start (pool/model-relay missing)")
		return
	}
	go func() {
		t := time.NewTicker(defaultTickInterval)
		defer t.Stop()
		r.tick(ctx) // 启动立刻跑一次, 处理重启时段
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.tick(ctx)
			}
		}
	}()
	r.Logger.Info("weekly digest runner started",
		"interval", defaultTickInterval, "model", r.Model)
}

// tick — 每 5min 醒一次, 检查是否到了 "周日 8 点之后", 没到就跳过.
// 到了之后扫描应该跑但还没跑的用户, 一次最多处理 50 个 (避免一波打挂
// model-relay).
func (r *Runner) tick(ctx context.Context) {
	now := time.Now().UTC()
	if !shouldRunNow(now) {
		return
	}
	week := isoWeek(now)
	users, err := r.findPendingUsers(ctx, week)
	if err != nil {
		r.Logger.Warn("weekly: find pending users", "err", err.Error())
		return
	}
	if len(users) == 0 {
		return
	}
	r.Logger.Info("weekly: tick", "iso_week", week, "users_pending", len(users))
	for _, uid := range users {
		if err := r.runForUser(ctx, uid, week); err != nil {
			r.Logger.Warn("weekly: user", "user_id", uid, "err", err.Error())
		}
	}
}

// shouldRunNow — 周日 (UTC) 且 hour ≥ 8. 早一点点的 tick 直接跳过, 等下
// 一轮 5min 后再来.
func shouldRunNow(now time.Time) bool {
	if now.Weekday() != time.Sunday {
		return false
	}
	return now.Hour() >= 8
}

// isoWeek — "YYYY-Www" 格式, e.g. "2026-W24".
func isoWeek(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// findPendingUsers — 上周 7d 内有 starred/read/wiki 行为且本周还没跑过的
// 用户. LIMIT 50 一波.
func (r *Runner) findPendingUsers(ctx context.Context, week string) ([]string, error) {
	rows, err := r.Pool.Query(ctx, `
		WITH active AS (
			SELECT DISTINCT f.scope_id AS user_id
			  FROM rss.feeds f
			  JOIN rss.entries e ON e.feed_id = f.id
			 WHERE f.scope = 'user'
			   AND (
			     e.starred = true
			     OR e.read_at IS NOT NULL
			   )
			   AND COALESCE(e.read_at, e.fetched_at) > now() - interval '7 days'
		)
		SELECT a.user_id
		  FROM active a
		 WHERE NOT EXISTS (
		       SELECT 1 FROM rss.weekly_runs wr
		        WHERE wr.user_id = a.user_id AND wr.iso_week = $1
		 )
		 LIMIT 50`, week)
	if err != nil {
		return nil, fmt.Errorf("weekly: query pending: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

// userStats — 单用户 7d 行为聚合.
type userStats struct {
	StarredN int
	ReadN    int
	WikiN    int
	TopTopics []string // top 3
	TopEntries []entryBrief // top 5 (按 importance + starred 加权)
}

type entryBrief struct {
	Title    string
	URL      string
	FeedTitle string
	Takeaway string
}

func (r *Runner) collectStats(ctx context.Context, userID string) (*userStats, error) {
	st := &userStats{}
	// counts
	err := r.Pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE e.starred=true AND e.fetched_at > now() - interval '7 days'),
		  COUNT(*) FILTER (WHERE e.read_at IS NOT NULL AND e.read_at > now() - interval '7 days'),
		  (SELECT COUNT(*) FROM rss.entry_marks em
		    JOIN rss.entries e2 ON e2.id = em.entry_id
		    JOIN rss.feeds f2 ON f2.id = e2.feed_id
		   WHERE em.mark='wiki' AND em.user_id=$1
		     AND em.created_at > now() - interval '7 days'
		     AND f2.scope='user' AND f2.scope_id=$1)
		  FROM rss.entries e
		  JOIN rss.feeds f ON f.id = e.feed_id
		 WHERE f.scope='user' AND f.scope_id=$1`,
		userID).Scan(&st.StarredN, &st.ReadN, &st.WikiN)
	if err != nil {
		return nil, fmt.Errorf("weekly: counts: %w", err)
	}

	// top topics (一周内 read/starred entries 的 ai_topics 聚合)
	tRows, err := r.Pool.Query(ctx, `
		SELECT topic, COUNT(*) AS n
		  FROM (
		    SELECT unnest(e.ai_topics) AS topic
		      FROM rss.entries e
		      JOIN rss.feeds   f ON f.id = e.feed_id
		     WHERE f.scope='user' AND f.scope_id=$1
		       AND COALESCE(e.read_at, e.fetched_at) > now() - interval '7 days'
		       AND e.ai_topics IS NOT NULL
		  ) x
		 GROUP BY topic
		 ORDER BY n DESC
		 LIMIT 3`, userID)
	if err != nil {
		return nil, fmt.Errorf("weekly: topics: %w", err)
	}
	defer tRows.Close()
	for tRows.Next() {
		var topic string
		var n int
		if err := tRows.Scan(&topic, &n); err == nil && topic != "" {
			st.TopTopics = append(st.TopTopics, topic)
		}
	}

	// top 5 entries (importance desc + starred boost)
	eRows, err := r.Pool.Query(ctx, `
		SELECT e.title, e.url, COALESCE(f.title,''),
		       COALESCE(NULLIF(e.ai_takeaway,''), '')
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE f.scope='user' AND f.scope_id=$1
		   AND COALESCE(e.read_at, e.fetched_at) > now() - interval '7 days'
		 ORDER BY (CASE WHEN e.starred THEN 100 ELSE 0 END
		           + COALESCE(e.ai_importance, 0)) DESC
		 LIMIT $2`, userID, maxTopEntries)
	if err != nil {
		return nil, fmt.Errorf("weekly: top entries: %w", err)
	}
	defer eRows.Close()
	for eRows.Next() {
		var b entryBrief
		if err := eRows.Scan(&b.Title, &b.URL, &b.FeedTitle, &b.Takeaway); err == nil {
			st.TopEntries = append(st.TopEntries, b)
		}
	}
	return st, nil
}

// runForUser — 端到端处理一个用户. 失败时仍写一行 weekly_runs(error=...)
// 防止反复重试.
func (r *Runner) runForUser(ctx context.Context, userID, week string) error {
	stats, err := r.collectStats(ctx, userID)
	if err != nil {
		_ = r.recordRun(ctx, userID, week, "", "", err.Error())
		return err
	}
	// 用户没活动 (理论上 findPendingUsers 已过滤, 兜底)
	if stats.StarredN+stats.ReadN+stats.WikiN == 0 {
		_ = r.recordRun(ctx, userID, week, "", "", "no_activity")
		return nil
	}

	systemPrompt := buildSystemPrompt(stats)
	question := fmt.Sprintf("用 markdown 写一份不超过 300 字的「%s 上周回顾」, "+
		"包含 top 3 话题段, "+
		"再用 1-2 句概括接下来一周值得关注什么. "+
		"避免编造未列出的事实.", week)

	answer, err := r.callLLM(ctx, userID, systemPrompt, question)
	if err != nil {
		_ = r.recordRun(ctx, userID, week, "", "", err.Error())
		return err
	}

	pageID := ""
	if r.BrainURL != "" {
		pid, perr := r.persistWiki(ctx, userID, week, answer, stats)
		if perr != nil {
			r.Logger.Warn("weekly: wiki persist failed",
				"user_id", userID, "err", perr.Error())
		} else {
			pageID = pid
		}
	}

	summary := buildSummaryHeader(stats)
	_ = r.recordRun(ctx, userID, week, pageID, summary, "")
	r.Logger.Info("weekly: ok",
		"user_id", userID, "week", week,
		"chars", len(answer), "page_id", pageID)
	return nil
}

func buildSystemPrompt(s *userStats) string {
	var sb strings.Builder
	sb.WriteString("你是 BiuMind RSS 的周报作者. 基于下面的统计 + entry 列表 ")
	sb.WriteString("写一份温和、克制、易读的 markdown 周报. 不要编造未列出的事实.\n\n")
	fmt.Fprintf(&sb, "本周统计: starred=%d, read=%d, wiki=%d.\n",
		s.StarredN, s.ReadN, s.WikiN)
	if len(s.TopTopics) > 0 {
		fmt.Fprintf(&sb, "Top 话题: %s.\n", strings.Join(s.TopTopics, ", "))
	}
	if len(s.TopEntries) > 0 {
		sb.WriteString("\n重点条目 (按重要度排序):\n")
		for i, e := range s.TopEntries {
			fmt.Fprintf(&sb, "[%d] %s (来源: %s)\n", i+1, e.Title, e.FeedTitle)
			if e.Takeaway != "" {
				fmt.Fprintf(&sb, "    %s\n", e.Takeaway)
			}
		}
	}
	return sb.String()
}

func buildSummaryHeader(s *userStats) string {
	parts := []string{}
	parts = append(parts, fmt.Sprintf("读 %d", s.ReadN))
	parts = append(parts, fmt.Sprintf("收藏 %d", s.StarredN))
	parts = append(parts, fmt.Sprintf("沉 wiki %d", s.WikiN))
	if len(s.TopTopics) > 0 {
		parts = append(parts, "话题: "+strings.Join(s.TopTopics, "/"))
	}
	return strings.Join(parts, " · ")
}

func (r *Runner) recordRun(ctx context.Context, userID, week, pageID, summary, errStr string) error {
	_, err := r.Pool.Exec(ctx, `
		INSERT INTO rss.weekly_runs (user_id, iso_week, page_id, summary, error)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (user_id, iso_week) DO UPDATE
		   SET ran_at  = now(),
		       page_id = EXCLUDED.page_id,
		       summary = EXCLUDED.summary,
		       error   = EXCLUDED.error`,
		userID, week, pageID, summary, errStr)
	return err
}

// callLLM — 跟 copilot 同一条路径: model-relay /v1/messages, OpenAI / Anthropic
// shape 兼容.
func (r *Runner) callLLM(ctx context.Context, userID, systemPrompt, question string) (string, error) {
	if r.SignFor == nil {
		return "", errors.New("weekly: signFor nil")
	}
	token, err := r.SignFor(userID)
	if err != nil {
		return "", fmt.Errorf("weekly: sign: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"model":      r.Model,
		"max_tokens": 1024,
		"system":     systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": question},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.ModelRelayURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := r.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("weekly: status %d: %s", resp.StatusCode,
			truncate(string(respBody), 200))
	}
	var openai struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(respBody, &openai) == nil && len(openai.Choices) > 0 {
		if c := openai.Choices[0].Message.Content; c != "" {
			return c, nil
		}
	}
	var anthropic struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &anthropic); err == nil {
		var sb []byte
		for _, c := range anthropic.Content {
			if c.Type == "text" {
				sb = append(sb, c.Text...)
			}
		}
		if len(sb) > 0 {
			return string(sb), nil
		}
	}
	return "", errors.New("weekly: empty LLM response")
}

// persistWiki — 简化路径: 用户 default project → 创建 page → append paragraph
// block. 跟 actions/wiki appender 一样的访问模式; 未来抽公共 client.
func (r *Runner) persistWiki(ctx context.Context, userID, week, markdown string, _ *userStats) (string, error) {
	token, err := r.SignFor(userID)
	if err != nil {
		return "", err
	}
	pid, err := r.ensureProject(ctx, token, "Inbox")
	if err != nil {
		return "", err
	}
	pgID, err := r.createPage(ctx, token, pid,
		fmt.Sprintf("RSS 周报 %s", week))
	if err != nil {
		return "", err
	}
	if err := r.appendParagraph(ctx, token, pid, pgID, markdown); err != nil {
		return "", err
	}
	return pgID, nil
}

func (r *Runner) ensureProject(ctx context.Context, token, name string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		r.BrainURL+"/v1/wiki/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("list status %d: %s", resp.StatusCode, body)
	}
	var listed struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	_ = json.Unmarshal(body, &listed)
	for _, p := range listed.Projects {
		if p.Name == name {
			return p.ID, nil
		}
	}
	createBody, _ := json.Marshal(map[string]any{"name": name})
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		r.BrainURL+"/v1/wiki/projects", bytes.NewReader(createBody))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := r.HTTP.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(io.LimitReader(resp2.Body, 64*1024))
	if resp2.StatusCode >= 300 {
		return "", fmt.Errorf("create status %d: %s", resp2.StatusCode, body2)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body2, &created)
	return created.ID, nil
}

func (r *Runner) createPage(ctx context.Context, token, pid, title string) (string, error) {
	body, _ := json.Marshal(map[string]any{"title": title})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		r.BrainURL+"/v1/wiki/projects/"+pid+"/pages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("create page status %d: %s",
			resp.StatusCode, respBody)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(respBody, &created)
	return created.ID, nil
}

func (r *Runner) appendParagraph(ctx context.Context, token, pid, pgID, markdown string) error {
	body, _ := json.Marshal(map[string]any{
		"type":    "paragraph",
		"content": map[string]any{"text": markdown},
	})
	url := r.BrainURL + "/v1/wiki/projects/" + pid + "/pages/" + pgID + "/blocks"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("append block status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
