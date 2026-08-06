// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package rss // import "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/rss"

import (
	"fmt"
	"io"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/model"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/xml"
)

// Parse returns a normalized feed struct from a RSS feed.
func Parse(baseURL string, data io.ReadSeeker) (*model.Feed, error) {
	rssFeed := new(rss)
	decoder := xml.NewXMLDecoder(data)
	decoder.DefaultSpace = "rss"
	if err := decoder.Decode(rssFeed); err != nil {
		return nil, fmt.Errorf("rss: unable to parse feed: %w", err)
	}
	adapter := &rssAdapter{rssFeed}
	return adapter.buildFeed(baseURL), nil
}
