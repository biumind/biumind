// feeds_discover — given any URL, detect source kind + resolve feed
// URL. Used by the add-feed sheet to show the user what they pasted
// will resolve to before they confirm.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (a *App) invokeFeedsDiscover(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.discover == nil {
		return nil, errors.New("rss: discover not wired")
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.URL == "" {
		return nil, errors.New("rss: url required")
	}
	r, err := a.discover.Discover(ctx, in.URL)
	if err != nil {
		return map[string]any{
			"original_url": in.URL,
			"kind":         string(KindUnknown),
			"feed_url":     "",
			"error":        err.Error(),
		}, nil
	}
	return map[string]any{
		"original_url": r.OriginalURL,
		"kind":         string(r.Kind),
		"feed_url":     r.FeedURL,
	}, nil
}
