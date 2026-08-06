// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package rss // import "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/rss"

// feedBurnerItemElement represents FeedBurner XML elements.
type feedBurnerItemElement struct {
	FeedBurnerLink          string `xml:"http://rssnamespace.org/feedburner/ext/1.0 origLink"`
	FeedBurnerEnclosureLink string `xml:"http://rssnamespace.org/feedburner/ext/1.0 origEnclosureLink"`
}
