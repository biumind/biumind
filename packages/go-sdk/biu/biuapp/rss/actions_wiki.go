// entries_to_wiki — sink an RSS entry into the user's BiuMind Wiki.
// Wired only when WikiSink is attached via WithWikiSink.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// WikiSink is the SDK-side surface; services wires brain Wiki client.
type WikiSink interface {
	// Sink writes an entry into the user's wiki and returns the
	// created page id + suggested tags. token is the user's bearer
	// (so brain creates the page under the user's project).
	Sink(ctx context.Context, token string, in WikiSinkInput) (*WikiSinkResult, error)
}

type WikiSinkInput struct {
	UserID      string
	EntryID     uuid.UUID
	Title       string
	URL         string
	Author      string
	FeedTitle   string
	ContentHTML string
	AITakeaway  string
	AIBullets   []string
	AITopics    []string
}

type WikiSinkResult struct {
	PageID         string   `json:"page_id"`
	ProjectID      string   `json:"project_id"`
	SuggestedTags  []string `json:"suggested_tags"`
}

// WithWikiSink wires the wiki sink. Optional; when set, the
// entries_to_wiki action is exposed.
func (a *App) WithWikiSink(s WikiSink) *App {
	a.wiki = s
	return a
}

func (a *App) invokeEntriesToWiki(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.wiki == nil {
		return nil, errors.New("rss: wiki sink not wired")
	}
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad entry id: %w", err)
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	token := bauth.RawTokenFrom(ctx)
	if token == "" {
		return nil, errors.New("rss: missing bearer in context")
	}

	// Caller must own a feed containing this entry — scope isolation.
	feeds, err := a.pg.ListFeeds(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	feedSet := map[uuid.UUID]string{}
	for _, f := range feeds {
		feedSet[f.ID] = f.Title
	}

	rows, err := a.pg.pool.Query(ctx, `
		SELECT title, COALESCE(url,''), COALESCE(author,''),
		       COALESCE(content_html,''), COALESCE(ai_takeaway,''),
		       COALESCE(ai_bullets, '[]'::jsonb), COALESCE(ai_topics, '{}'::text[]),
		       feed_id
		  FROM rss.entries WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	var (
		title, url, author, html, takeaway string
		bulletsJSON                        []byte
		topics                             []string
		feedID                             uuid.UUID
	)
	if err := rows.Scan(&title, &url, &author, &html, &takeaway,
		&bulletsJSON, &topics, &feedID); err != nil {
		return nil, err
	}
	feedTitle, ok := feedSet[feedID]
	if !ok {
		return nil, ErrNotFound
	}
	var bullets []string
	if len(bulletsJSON) > 0 {
		_ = json.Unmarshal(bulletsJSON, &bullets)
	}

	res, err := a.wiki.Sink(ctx, token, WikiSinkInput{
		UserID:      scopeID,
		EntryID:     id,
		Title:       title,
		URL:         url,
		Author:      author,
		FeedTitle:   feedTitle,
		ContentHTML: html,
		AITakeaway:  takeaway,
		AIBullets:   bullets,
		AITopics:    topics,
	})
	if err != nil {
		RecordWikiSink("error")
		return nil, err
	}
	RecordWikiSink("ok")

	// Mark in entry_marks so saved tab + UI can show "已沉".
	if res.PageID != "" {
		blockUUID, _ := uuid.Parse(res.PageID)
		_, _ = a.pg.pool.Exec(ctx, `
			INSERT INTO rss.entry_marks (user_id, entry_id, mark, wiki_block_id)
			VALUES ($1, $2, 'wiki', $3)
			ON CONFLICT (user_id, entry_id, mark) DO UPDATE
			   SET wiki_block_id = EXCLUDED.wiki_block_id`,
			scopeID, id, blockUUID)
	}

	return map[string]any{
		"page_id":        res.PageID,
		"project_id":     res.ProjectID,
		"suggested_tags": res.SuggestedTags,
	}, nil
}
