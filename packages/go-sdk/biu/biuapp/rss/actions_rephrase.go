// entries_rephrase — given an entry id and a persona, ask the LLM to
// retell the article in that persona's voice. v1 returns the full
// rewritten text in the response; streaming over Realtime SSE will
// land later (no architectural change needed; the client just
// re-binds the response source).

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// Personas the UI exposes. Add carefully — every entry rephrase is
// one LLM call, so a long list multiplies cost.
var rephrasePersonas = map[string]string{
	"child":   "用 5 岁小孩能听懂的话讲, 不要专业术语, 用比喻。",
	"layman":  "用非技术读者能听懂的话讲, 跳过细节, 强调影响。",
	"boss":    "电梯演讲风格, 30 秒讲完, 给出一句话结论 + 2 个支撑论据。",
	"expert":  "对同行专家, 详细技术细节, 不解释基础概念。",
	"english": "用流畅英文重写, 保留专有名词的英文原文。",
}

func (a *App) invokeEntriesRephrase(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.llm == nil {
		return nil, errors.New("rss: llm not wired")
	}
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		ID      string `json:"id"`
		Persona string `json:"persona"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad entry id: %w", err)
	}
	personaPrompt, ok := rephrasePersonas[in.Persona]
	if !ok {
		return nil, fmt.Errorf("rss: unknown persona %q", in.Persona)
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}

	// Caller must own a feed containing this entry — scope isolation.
	feeds, err := a.pg.ListFeeds(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	feedSet := map[uuid.UUID]bool{}
	for _, f := range feeds {
		feedSet[f.ID] = true
	}

	rows, err := a.pg.pool.Query(ctx, `
		SELECT title, COALESCE(content_html, ''), feed_id
		  FROM rss.entries WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	var title, html string
	var feedID uuid.UUID
	if err := rows.Scan(&title, &html, &feedID); err != nil {
		return nil, err
	}
	if !feedSet[feedID] {
		return nil, ErrNotFound
	}

	token := bauth.RawTokenFrom(ctx)
	if token == "" {
		return nil, errors.New("rss: missing bearer in context")
	}

	body := strings.TrimSpace(stripTags(html, 4000))
	if body == "" {
		body = title
	}

	rephrased, err := a.llm.Rephrase(ctx, token, title, personaPrompt, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":        id.String(),
		"persona":   in.Persona,
		"rephrased": rephrased,
	}, nil
}
