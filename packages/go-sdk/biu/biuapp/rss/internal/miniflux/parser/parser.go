// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package parser // import "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/parser"

import (
	"errors"
	"io"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/atom"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/json"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/model"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/rdf"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/rss"
)

var ErrFeedFormatNotDetected = errors.New("parser: unable to detect feed format")

// ParseFeed analyzes the input data and returns a normalized feed object.
func ParseFeed(baseURL string, r io.ReadSeeker) (*model.Feed, error) {
	format, version := DetectFeedFormat(r)
	switch format {
	case FormatAtom:
		return atom.Parse(baseURL, r, version)
	case FormatRSS:
		return rss.Parse(baseURL, r)
	case FormatJSON:
		return json.Parse(baseURL, r)
	case FormatRDF:
		return rdf.Parse(baseURL, r)
	default:
		return nil, ErrFeedFormatNotDetected
	}
}
