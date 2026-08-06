// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package json // import "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/json"

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/model"
)

// Parse returns a normalized feed struct from a JSON feed.
func Parse(baseURL string, data io.Reader) (*model.Feed, error) {
	jsonFeed := new(JSONFeed)
	if err := json.NewDecoder(data).Decode(jsonFeed); err != nil {
		return nil, fmt.Errorf("json: unable to parse feed: %w", err)
	}

	return NewJSONAdapter(jsonFeed).BuildFeed(baseURL), nil
}
