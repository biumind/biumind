// Chat 消息搜索 — POST /v1/chat/search.
//
// 设计文档: docs/BiuMind-Chat-Search-Design.md
//
// 走 search_vector + GIN (migration 00032). 严格 user_id scope。
// streaming/processing/pending/paused 状态消息不进索引, 自动排除。
//
// 高亮: ts_headline 包 <mark>...</mark>。前端按 partsJson 渲染时
// 注入到匹配位即可。

package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 100
	// snippetCap — ts_headline 在长内容上慢, 截断到 ~4KB 控制 cost。
	// 4KB 内已能涵盖 95% 消息全文, 超长 thinking/tool 块不进 snippet。
	searchSnippetCap = 4000
)

// SearchHit — 单条命中。
type SearchHit struct {
	MessageID   uuid.UUID `json:"message_id"`
	ThreadID    uuid.UUID `json:"thread_id"`
	ThreadTitle string    `json:"thread_title"`
	Role        string    `json:"role"`
	Snippet     string    `json:"snippet"`
	Score       float64   `json:"score"`
	CreatedAt   time.Time `json:"created_at"`
}

// SearchRequest — POST /v1/chat/search 请求体。
type SearchRequest struct {
	Query     string     `json:"query"`
	ThreadID  *uuid.UUID `json:"thread_id,omitempty"`
	Role      string     `json:"role,omitempty"`
	From      *time.Time `json:"from,omitempty"`
	To        *time.Time `json:"to,omitempty"`
	Limit     int        `json:"limit,omitempty"`
	Offset    int        `json:"offset,omitempty"`
	Highlight *bool      `json:"highlight,omitempty"`
}

// SearchResponse — POST /v1/chat/search 响应体。
type SearchResponse struct {
	Hits   []SearchHit `json:"hits"`
	Total  int         `json:"total"`
	TookMs int64       `json:"took_ms"`
	Query  string      `json:"query"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	var in SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	in.Query = trimWhitespace(in.Query)
	if in.Query == "" {
		writeErr(w, http.StatusBadRequest, "bad_query", "query required")
		return
	}
	if len([]rune(in.Query)) > 200 {
		writeErr(w, http.StatusBadRequest, "bad_query", "query too long (max 200 chars)")
		return
	}
	if in.Role != "" {
		switch in.Role {
		case "user", "assistant", "tool", "system":
		default:
			writeErr(w, http.StatusBadRequest, "bad_filter",
				fmt.Sprintf("role must be one of user|assistant|tool|system, got %q", in.Role))
			return
		}
	}
	if in.Limit <= 0 {
		in.Limit = searchDefaultLimit
	}
	if in.Limit > searchMaxLimit {
		writeErr(w, http.StatusBadRequest, "limit_too_high",
			fmt.Sprintf("limit max %d", searchMaxLimit))
		return
	}
	if in.Offset < 0 {
		in.Offset = 0
	}
	highlight := true
	if in.Highlight != nil {
		highlight = *in.Highlight
	}

	startedAt := time.Now()
	hits, total, err := s.Store.SearchMessages(r.Context(), uid, in, highlight)
	if err != nil {
		s.Logger.Error("chat search failed",
			"user", uid, "query", in.Query, "err", err)
		writeErr(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, SearchResponse{
		Hits:   hits,
		Total:  total,
		TookMs: time.Since(startedAt).Milliseconds(),
		Query:  in.Query,
	})
}

// trimWhitespace — strings.TrimSpace 替身, 这里包一层避免 strings 在
// 个文件多次 import 引入歧义。
func trimWhitespace(s string) string {
	for len(s) > 0 && isSpace(s[0]) {
		s = s[1:]
	}
	for len(s) > 0 && isSpace(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// ─── Store implementation ─────────────────────────────────

// SearchMessages — 走 search_vector + ts_rank, 二级 created_at DESC。
// total 通过 COUNT(*) over () window 函数同行返回, 避免二次查询。
func (st *Store) SearchMessages(
	ctx context.Context,
	userID uuid.UUID,
	req SearchRequest,
	highlight bool,
) ([]SearchHit, int, error) {
	// ts_headline 比较贵; highlight=false 时退回 plain left() 截断。
	// 两条 expr 都引用 $9 (snippet cap) 保持参数 arity 不变。
	headlineExpr := `ts_headline('biumind_zhcn',
                left(coalesce(m.content, ''), $9),
                q.tsq,
                'StartSel=<mark>, StopSel=</mark>, MaxFragments=2, MaxWords=20, MinWords=5')`
	if !highlight {
		headlineExpr = `left(coalesce(m.content, ''), least(200, $9))`
	}

	const sqlTpl = `
WITH q AS (
  SELECT plainto_tsquery('biumind_zhcn', $1) AS tsq
)
SELECT
    m.id, m.thread_id, COALESCE(t.title, ''), m.role, m.created_at,
    -- ts_rank normalization=1 → divide by 1 + log(length). 让短消息
    -- 中精确命中排在长消息中"埋在噪音里"的命中之前。
    ts_rank(m.search_vector, q.tsq, 1) AS score,
    %s AS snippet,
    COUNT(*) OVER () AS total
  FROM chat.messages m
  JOIN chat.threads  t ON t.id = m.thread_id
  CROSS JOIN q
 WHERE m.user_id = $2
   AND m.status IN ('success', 'error')
   AND m.search_vector @@ q.tsq
   AND ($3::uuid IS NULL OR m.thread_id = $3)
   AND ($4::text IS NULL OR m.role = $4)
   AND ($5::timestamptz IS NULL OR m.created_at >= $5)
   AND ($6::timestamptz IS NULL OR m.created_at <= $6)
 ORDER BY score DESC, m.created_at DESC
 LIMIT $7 OFFSET $8
`
	sqlStr := fmt.Sprintf(sqlTpl, headlineExpr)

	var roleArg any
	if req.Role != "" {
		roleArg = req.Role
	}
	var threadArg any
	if req.ThreadID != nil {
		threadArg = *req.ThreadID
	}
	var fromArg any
	if req.From != nil {
		fromArg = *req.From
	}
	var toArg any
	if req.To != nil {
		toArg = *req.To
	}

	rows, err := st.pool.Query(ctx, sqlStr,
		req.Query, userID, threadArg, roleArg, fromArg, toArg,
		req.Limit, req.Offset, searchSnippetCap)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var hits []SearchHit
	var total int
	for rows.Next() {
		var h SearchHit
		var rowTotal int
		if err := rows.Scan(
			&h.MessageID, &h.ThreadID, &h.ThreadTitle, &h.Role, &h.CreatedAt,
			&h.Score, &h.Snippet, &rowTotal,
		); err != nil {
			return nil, 0, err
		}
		hits = append(hits, h)
		total = rowTotal
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if hits == nil {
		hits = []SearchHit{}
	}
	// total = 0 时 (没匹配) total 直接给 0; 跳过零结果时 COUNT 不被求值。
	_ = pgx.ErrNoRows // 文档锚点 — pgx import 仍需要给上面的 Query 用
	return hits, total, nil
}
