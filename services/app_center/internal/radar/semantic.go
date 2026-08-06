// SQL-native cosine 雷达 (M8.2 v3).
//
// 设计选择: 不在 Go 内存里跑 cosine. pgvector 的 <=> 操作符 + ivfflat
// 索引是 ANN 加速过的, 一次 query 就能算所有 (rule, entry) 对的距离.
// 比从 DB 拉 embedding 到 Go 再算快 2-3 个数量级 (尤其是雷达 tick 频
// 繁运行时).
//
// 为什么不并入 MatchBatch:
//   - MatchBatch 是纯函数 (rules + candidates → hits), 不带 ctx/DB
//   - cosine 必须查 DB (拿 entries.embedding), 让 MatchBatch 接 DB 会
//     破坏它的纯函数性质 + 单测复杂度
//   - 触发场景不同: keyword 是 fetcher 推 candidate 即时算; cosine 是
//     embedding worker 写完后定时跑(或被推动)
//
// 调度: app_center main.go 在 radar tick 末尾(keyword 之后)调一次
// SemanticBatch. 它读最近 N 分钟内有 embedding 的 entries 跨所有 enabled
// rule 算 cosine, 命中即 INSERT watch_hits.
//
// 重复防御: 进入 SemanticBatch 前已用 ScanWindow 限定时间窗口; cooldown
// 由调用方继续走 FilterCooldown (跟 keyword 路径一样, 复用一份逻辑).

package radar

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SemanticBatch 一次 SQL 算所有 (enabled rule with semantic_embedding,
// recent entry with embedding) 的 cosine, 返回距离 ≤ 1 - threshold 的
// 候选 hits. Caller 后续走 FilterCooldown + AggregateForBurst + WriteHits,
// 跟 keyword 路径同源.
//
// scanWindow: 只看最近多久写入 embedding 的 entries. 推荐 30min — 跟
// embedding worker 的 backfill 周期 (5min) 拉开, 既不漏单也不重复打
// 翻一遍历史. 0 = 不限.
func (s *Store) SemanticBatch(ctx context.Context, scanWindow time.Duration) ([]Hit, error) {
	// 1 - cosine_distance >= threshold  ⟺  cosine_distance <= 1 - threshold
	// pgvector <=> 是 cosine_distance.
	//
	// 注意 r.semantic_threshold 默认 0.78, 也就是要相似度 ≥ 0.78 才命中.
	// embedding 没有就跳过 (semantic_embedding IS NOT NULL).
	//
	// 跨 scope 安全: rule.scope_id 必须 = feed.scope_id (同一用户的 rule
	// 只看自己 feed; org rule 看 org feed). 全局 scope 留待 v3 M11.
	q := `
		SELECT r.id AS rule_id,
		       e.title,
		       e.url,
		       e.hash AS title_hash,
		       'rss:' || f.id::text AS source,
		       (1 - (r.semantic_embedding <=> e.embedding))::real AS sim
		  FROM rss.watch_rules r
		  JOIN rss.feeds f
		    ON f.scope=r.scope AND f.scope_id=r.scope_id AND f.enabled
		  JOIN rss.entries e
		    ON e.feed_id=f.id
		 WHERE r.enabled = true
		   AND r.semantic_embedding IS NOT NULL
		   AND e.embedding IS NOT NULL
		   AND ($1::interval IS NULL OR e.fetched_at > now() - $1::interval)
		   AND (1 - (r.semantic_embedding <=> e.embedding)) >= COALESCE(r.semantic_threshold, 0.78)
		 ORDER BY sim DESC
		 LIMIT 500
	`
	var arg any
	if scanWindow > 0 {
		arg = scanWindow.String()
	} else {
		arg = nil
	}
	rows, err := s.pool.Query(ctx, q, arg)
	if err != nil {
		return nil, fmt.Errorf("radar: semantic batch: %w", err)
	}
	defer rows.Close()

	// 拉 rule snapshots 一次 (cooldown / dispatch 需要 OnHitBadge 等).
	ruleByID, err := s.loadRulesByID(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Hit, 0, 64)
	for rows.Next() {
		var (
			ruleID    uuid.UUID
			title     string
			url       string
			titleHash []byte
			source    string
			sim       float32
		)
		if err := rows.Scan(&ruleID, &title, &url, &titleHash, &source, &sim); err != nil {
			return nil, fmt.Errorf("radar: semantic scan: %w", err)
		}
		r := ruleByID[ruleID]
		if r == nil {
			continue
		}
		out = append(out, Hit{
			RuleID:       ruleID,
			Source:       source,
			Title:        title,
			URL:          url,
			TitleHash:    titleHash,
			RuleSnapshot: *r,
		})
	}
	return out, rows.Err()
}

// loadRulesByID — caller 通常已经持有 enabled rules 列表, 但 SemanticBatch
// 只拿 rule_id, 这里补一次 RuleSnapshot 给 dispatcher 用.
//
// 优化空间: 调用方传入 rules slice 可省一次 query. 当前 rule 数 < 1k,
// 全表扫一次 < 1ms, 不值得做.
func (s *Store) loadRulesByID(ctx context.Context) (map[uuid.UUID]*Rule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+` FROM rss.watch_rules WHERE enabled=true`)
	if err != nil {
		return nil, fmt.Errorf("radar: load rules by id: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*Rule, 32)
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}
