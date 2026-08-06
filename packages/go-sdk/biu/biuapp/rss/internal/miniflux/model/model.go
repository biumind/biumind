// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package model // import "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss/internal/miniflux/model"

type Number interface {
	int | int64 | float64
}

func OptionalNumber[T Number](value T) *T {
	if value > 0 {
		return &value
	}
	return nil
}

func OptionalString(value string) *string {
	if value != "" {
		return &value
	}
	return nil
}

// VENDOR-NOTE: upstream uses Go 1.26 `new(value)` form; rewritten for 1.25.
func SetOptionalField[T any](value T) *T {
	v := value
	return &v
}
