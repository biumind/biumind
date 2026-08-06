// Package biumind is the Go SDK for the BiuMind Agentics platform.
//
// Two clients share a Config:
//
//   - RelayClient   — Anthropic-compatible relay (`POST /v1/messages`)
//   - MemoryClient — Brain memory service (`/v1/memory*`)
//
// Both use net/http only; no third-party deps.
//
// Example:
//
//	cfg, _ := biumind.LoadConfig() // BIUMIND_MODEL_RELAY_URL + BIUMIND_TOKEN
//	relay := biumind.NewRelayClient(cfg)
//	stream, err := relay.MessagesStream(ctx, biumind.MessagesRequest{
//		Model: "claude-3-5-sonnet-latest",
//		Messages: []biumind.Message{{Role: "user", Content: "hi"}},
//	})
//	for chunk := range stream { fmt.Print(chunk) }
package biumind

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config bundles the connection settings every client needs.
type Config struct {
	RelayURL   string
	BrainURL string // defaults to RelayURL when empty
	Token    string
	Timeout  time.Duration

	// HTTPClient overrides the underlying HTTP client. When nil, a
	// client with Timeout is constructed for non-streaming calls; the
	// streaming path always uses a client with Timeout=0 because SSE
	// is open-ended.
	HTTPClient *http.Client
}

func (c *Config) normalize() {
	c.RelayURL = strings.TrimRight(c.RelayURL, "/")
	if c.BrainURL == "" {
		c.BrainURL = c.RelayURL
	} else {
		c.BrainURL = strings.TrimRight(c.BrainURL, "/")
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
}

// LoadConfig reads BIUMIND_MODEL_RELAY_URL / BIUMIND_TOKEN / BIUMIND_BRAIN_URL.
func LoadConfig() (Config, error) {
	relay := strings.TrimSpace(os.Getenv("BIUMIND_MODEL_RELAY_URL"))
	tok := strings.TrimSpace(os.Getenv("BIUMIND_TOKEN"))
	if relay == "" {
		return Config{}, errors.New("BIUMIND_MODEL_RELAY_URL is required")
	}
	if tok == "" {
		return Config{}, errors.New("BIUMIND_TOKEN is required")
	}
	cfg := Config{
		RelayURL:   relay,
		BrainURL: strings.TrimSpace(os.Getenv("BIUMIND_BRAIN_URL")),
		Token:    tok,
	}
	cfg.normalize()
	return cfg, nil
}

// ─── Errors ─────────────────────────────────────────────

// Error wraps an HTTP failure from model-relay or Brain.
type Error struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200]
	}
	return fmt.Sprintf("biumind: http %d: %s", e.Status, body)
}

// IsAuth returns true when the underlying status is 401 or 403.
func (e *Error) IsAuth() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// IsRateLimit returns true on a 429 response.
func (e *Error) IsRateLimit() bool { return e.Status == http.StatusTooManyRequests }

// IsNotFound returns true on a 404 response.
func (e *Error) IsNotFound() bool { return e.Status == http.StatusNotFound }
