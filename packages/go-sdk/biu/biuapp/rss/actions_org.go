// M11.4 — org admin forced subscriptions.
//
// An org admin pushes a feed to the whole org: it's stored as
// (scope='org', scope_id=org_id, forced=true). Every member's
// feeds_list unions in the org's forced feeds (see invokeFeedsList), and
// members cannot remove them (RemoveFeed refuses forced rows). The write
// is gated by rss:org_write via resolveScope(write=true), so only org
// admins reach the INSERT.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

func (a *App) invokeOrgFeedsForceAdd(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		FeedURL string `json:"feed_url"`
		Title   string `json:"title,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.FeedURL == "" {
		return nil, errors.New("rss: org_feeds_force_add requires feed_url")
	}
	// Force-add is an org write → org admins only (policies.cedar RSS 节).
	scope, scopeID, err := a.resolveScope(ctx, "org", true)
	if err != nil {
		return nil, err
	}

	// Validate + enrich by fetching once (same pattern as feeds_add) so
	// the forced feed shows a real title/icon immediately.
	title := in.Title
	siteURL, desc, iconURL := "", "", ""
	var firstFetch *FetchResult
	if a.fetcher != nil {
		if res, fErr := a.fetcher.Fetch(ctx, FetchRequest{FeedURL: in.FeedURL}); fErr == nil {
			firstFetch = res
			if title == "" {
				title = res.Title
			}
			siteURL, desc, iconURL = res.SiteURL, res.Description, res.IconURL
		}
	}
	if title == "" {
		title = in.FeedURL
	}

	f, err := a.pg.AddFeed(ctx, AddFeedInput{
		Scope: scope, ScopeID: scopeID,
		FeedURL: in.FeedURL, Title: title,
		SiteURL: siteURL, Description: desc, IconURL: iconURL,
		Forced: true,
	})
	if err != nil {
		return nil, err
	}
	if firstFetch != nil && !firstFetch.NotModified {
		_, _ = a.pg.InsertEntries(ctx, f.ID, firstFetch.Entries)
		_ = a.pg.UpdateFetchState(ctx, f.ID, FetchOutcome{
			Status: "ok", Etag: firstFetch.Etag, LastModified: firstFetch.LastModified,
			Title: firstFetch.Title, SiteURL: firstFetch.SiteURL, IconURL: firstFetch.IconURL,
		})
		if updated, gErr := a.pg.GetFeed(ctx, f.ID); gErr == nil {
			f = updated
		}
	}
	return feedJSON(f), nil
}

func (a *App) invokeOrgFeedsForceRemove(ctx context.Context, raw json.RawMessage) (any, error) {
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
		return nil, fmt.Errorf("rss: bad feed id: %w", err)
	}
	_, scopeID, err := a.resolveScope(ctx, "org", true)
	if err != nil {
		return nil, err
	}
	if err := a.pg.RemoveForcedFeed(ctx, scopeID, id); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
