// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package config is a SHIM for the upstream miniflux.app/v2/internal/config
// package. The real config bag is enormous and tightly coupled to Miniflux's
// runtime / CLI. The vendored reader/sanitizer/rewrite packages only call a
// handful of methods on a global `Opts` value; we satisfy that surface here
// with sensible defaults (no media proxy, no YouTube/Invidious rewrites).
//
// If you ever need to wire the full feature set in BiuMind, replace these
// returns with real values driven by your own config — do NOT vendor the
// upstream config package.
package config

// Options is the shimmed config container used by the vendored reader code.
// It is exported as a struct (rather than a pointer-to-private) because the
// upstream tests assign `config.Opts = config.NewConfigOptions()`, which
// requires the type to be visible.
type Options struct{}

// Methods actually called by the vendored code (sanitizer.go, rewrite/*).
// All return zero / disabled values.
func (o *Options) InvidiousInstance() string       { return "" }
func (o *Options) YouTubeEmbedDomain() string      { return "" }
func (o *Options) YouTubeEmbedUrlOverride() string { return "" }

// NewConfigOptions returns a fresh Options with default (disabled) values.
// Used by the vendored *_test.go files.
func NewConfigOptions() *Options { return &Options{} }

// Parser is a no-op stand-in for upstream's environment-variable config parser.
type Parser struct{}

// NewConfigParser returns a Parser. Used by the vendored *_test.go files.
func NewConfigParser() *Parser { return &Parser{} }

// ParseEnvironmentVariables ignores the environment and returns default options.
// The vendored tests only need a non-error return value.
func (p *Parser) ParseEnvironmentVariables() (*Options, error) {
	return NewConfigOptions(), nil
}

// Opts is the global config singleton expected by the vendored code.
var Opts = NewConfigOptions()
