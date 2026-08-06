// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package atom // import "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/atom"

import (
	"fmt"
	"io"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/model"
	xml_decoder "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/xml"
)

// Parse returns a normalized feed struct from a Atom feed.
func Parse(baseURL string, r io.ReadSeeker, version string) (*model.Feed, error) {
	switch version {
	case "0.3":
		atomFeed := new(atom03Feed)
		if err := xml_decoder.NewXMLDecoder(r).Decode(atomFeed); err != nil {
			return nil, fmt.Errorf("atom: unable to parse Atom 0.3 feed: %w", err)
		}
		adapter := &atom03Adapter{atomFeed}
		return adapter.buildFeed(baseURL), nil
	default:
		atomFeed := new(atom10Feed)
		if err := xml_decoder.NewXMLDecoder(r).Decode(atomFeed); err != nil {
			return nil, fmt.Errorf("atom: unable to parse Atom 1.0 feed: %w", err)
		}
		adapter := &atom10Adapter{atomFeed}
		return adapter.BuildFeed(baseURL), nil
	}
}
