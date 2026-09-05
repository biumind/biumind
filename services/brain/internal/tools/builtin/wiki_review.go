package builtin

// wiki_review.go — wiki_create_review tool (S3 P0-3, 差距分析 D1)。
//
// reference/llm_wiki 的 review 队列 70% 内容来自 ingest/agent 产出
// （contradiction / duplicate / missing-page / suggestion 四类）；biumind
// 此前 review_items 的生产者只有 lint / dedup / sweep / 语义 lint 四个
// 周期检测器，agent loop 发现矛盾只能在最终总结里提一嘴，不落库、不进
// 审阅队列。本工具补上这个生产入口：agent loop 运行中发现页面间矛盾 /
// 内容缺口 / 疑似重复 / 改进建议时直接写 brain.review_items，用户在
// 清理后台统一审阅。
//
// kind=suggestion 由此顺带有了第一个生产者（D2）。语义 lint 的
// suggestion 落 kind=lint 的行为不变（两类生产者键空间不同：
// semantic:* vs agent:*，互不冲突）。
//
// Owner-scope 与写工具三件套（wiki_write.go）同模式：project / 每个
// page 都过 checkProjectOwned / checkPageOwned，跨 owner 一律
// "not found"。dedupe_key 不接受 agent 输入 —— 服务端按
// reviews.AgentKey(kind + 排序页面 + 归一标题) 归一计算，重复写幂等。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/tools"
	wikireviews "github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
)

// WikiCreateReview returns the review-queue write tool. Agent loop uses it
// to persist findings (contradiction / dedup / suggestion / lint ...) into
// brain.review_items instead of only mentioning them in the final summary.
// ReadOnly=false → biumindkit treats as side-effecting.
func WikiCreateReview(st *wikistore.Store, rv *wikireviews.Store) tools.Tool {
	schema := json.RawMessage(`{
  "type": "object",
  "properties": {
    "project_id": {"type": "string", "description": "UUID of the project the finding belongs to."},
    "kind": {"type": "string", "description": "Review kind, one of: contradiction (pages disagree), dedup (pages look like duplicates — but if you are confident they should merge, use wiki_merge_pages directly), suggestion (concrete improvement idea), lint (hygiene issue e.g. missing page for a referenced concept), sweep, merge."},
    "title": {"type": "string", "description": "Short finding title shown in the review queue. Required."},
    "summary": {"type": "string", "description": "Longer explanation: what the finding is, which claims/pages conflict, and what the user should check."},
    "page_ids": {"type": "array", "items": {"type": "string"}, "description": "UUIDs of the pages involved. All must belong to project_id. May be empty for project-level findings (e.g. a missing page)."}
  },
  "required": ["project_id", "kind", "title"]
}`)
	return tools.Tool{
		Descriptor: tools.Descriptor{
			Name:        "wiki_create_review",
			Description: "File a finding into the project's review queue (brain.review_items) for the user to triage. Use when you notice contradictory claims across pages, suspected duplicate pages you are NOT confident enough to merge, missing pages for referenced concepts, or concrete improvement suggestions. Do NOT use it for things you can fix yourself with the write tools. Writes are idempotent: the same finding (kind + pages + title) written twice produces one row.",
			Source:      "builtin",
			InputSchema: schema,
			Runtime:     tools.RuntimeCloud,
		},
		ReadOnly: false,
		Invoke: func(ctx context.Context, raw json.RawMessage) (any, error) {
			uid := tools.UserIDFromContext(ctx)
			if uid == uuid.Nil {
				return nil, errors.New("wiki_create_review: missing user identity in context")
			}
			var a struct {
				ProjectID string   `json:"project_id"`
				Kind      string   `json:"kind"`
				Title     string   `json:"title"`
				Summary   string   `json:"summary"`
				PageIDs   []string `json:"page_ids"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("wiki_create_review: bad input: %w", err)
			}
			// 纯参数校验放在任何 store 调用之前（无 DB 也可单测）。
			a.Kind = strings.TrimSpace(a.Kind)
			if !wikireviews.ValidKind(a.Kind) {
				return nil, fmt.Errorf("wiki_create_review: invalid kind %q", a.Kind)
			}
			a.Title = strings.TrimSpace(a.Title)
			if a.Title == "" {
				return nil, errors.New("wiki_create_review: title is required")
			}
			pid, err := checkProjectOwned(ctx, st, uid, a.ProjectID)
			if err != nil {
				return nil, fmt.Errorf("wiki_create_review: %w", err)
			}
			pageIDs := make([]uuid.UUID, 0, len(a.PageIDs))
			for _, rawID := range a.PageIDs {
				pageID, err := uuid.Parse(rawID)
				if err != nil {
					return nil, errors.New("wiki_create_review: page_ids must be UUIDs")
				}
				page, err := checkPageOwned(ctx, st, uid, pageID)
				if err != nil {
					return nil, fmt.Errorf("wiki_create_review: %w", err)
				}
				if page.ProjectID != pid {
					return nil, errors.New("wiki_create_review: all page_ids must belong to project_id")
				}
				pageIDs = append(pageIDs, pageID)
			}
			item, created, err := rv.Upsert(ctx, wikireviews.UpsertInput{
				ProjectID:   pid,
				OwnerID:     uid,
				Kind:        a.Kind,
				Title:       a.Title,
				Description: strings.TrimSpace(a.Summary),
				PageIDs:     pageIDs,
				Payload: map[string]any{
					"rule_family": "agent",
					"rule_id":     "agent:" + a.Kind,
				},
				DedupeKey: wikireviews.AgentKey(pid, a.Kind, pageIDs, a.Title),
			})
			if err != nil {
				return nil, fmt.Errorf("wiki_create_review: %w", err)
			}
			return map[string]any{
				"id":         item.ID.String(),
				"kind":       item.Kind,
				"status":     item.Status,
				"created":    created,
				"dedupe_key": item.DedupeKey,
			}, nil
		},
	}
}
