package reviews

// agent_key.go — dedupe_key scheme for review items written by the wiki
// agent loop's wiki_create_review tool (S3 P0-3, D1 gap: ingest/agent 侧
// 零 review 产出，补一个生产入口）。
//
// 与 DedupKeyForPair / semanticDedupeKey 同一思路：稳定输入 → 稳定键，
// agent 重跑 / 重复调用同一条发现不产重复行（dedupe_key UNIQUE 幂等）。

import (
	"sort"
	"strings"

	"github.com/google/uuid"
)

// AgentKey generates the canonical dedupe_key for an agent-written review.
// 键 = agent:<project>:<kind>:<hash(sorted page ids | normalized title)>：
//   - projectID 入键：dedupe_key 是全表 UNIQUE（非 per-project），不同
//     项目的同标题发现不能互相吞掉。
//   - pageIDs 排序：同一组页面不同顺序算同一条。
//   - title 归一（小写 + 折叠空白）：agent 措辞轻微波动不产新键；但标题
//     本质不同（不同发现）键不同 —— 与 semantic 用 title|detail 同理。
func AgentKey(projectID uuid.UUID, kind string, pageIDs []uuid.UUID, title string) string {
	ids := make([]string, 0, len(pageIDs))
	for _, id := range pageIDs {
		ids = append(ids, id.String())
	}
	sort.Strings(ids)
	norm := strings.ToLower(strings.Join(strings.Fields(title), " "))
	h := hashSubKey(strings.Join(ids, ",") + "|" + norm)
	return "agent:" + projectID.String() + ":" + kind + ":" + h
}
