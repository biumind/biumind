// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package rdf // import "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/rdf"

import (
	"fmt"
	"io"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/model"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/xml"
)

// Parse returns a normalized feed struct from a RDF feed.
func Parse(baseURL string, data io.ReadSeeker) (*model.Feed, error) {
	xmlFeed := new(rdf)
	if err := xml.NewXMLDecoder(data).Decode(xmlFeed); err != nil {
		return nil, fmt.Errorf("rdf: unable to parse feed: %w", err)
	}

	adapter := &rdfAdapter{xmlFeed}
	return adapter.buildFeed(baseURL), nil
}
