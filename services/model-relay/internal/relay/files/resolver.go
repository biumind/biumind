// Package files — model-relay-side helpers for resolving BiuMind file references
// (source.type=file, file_id=<uuid>) into something an upstream LLM can
// actually consume.
//
// 设计文档: docs/BiuMind-Chat-Attachments-MinIO-Design.md §4.6.
//
// Two backends:
//
//   - Resolver.PresignURL — primary path. Calls brain
//     POST /v1/files/{id}/presign-get and returns a short-lived (15min)
//     https URL. Cheap (a single round-trip, no bytes), the result fits
//     into the upstream provider's image-url slot. Used by Anthropic
//     (source.type=url) and OpenAI (image_url.url).
//
//   - Resolver.Fetch — fallback. model-relay fetches the bytes itself via
//     GET /v1/files/{id} for cases where the upstream LLM doesn't
//     accept URLs OR the URL fetch failed and we'd rather pay the
//     bandwidth than 4xx the user. Returns (bytes, mediaType).
//
// Auth: forward the user's bearer JWT (already on the inbound model-relay
// request). model-relay + brain share the JWT verifier (SelectVerifier), so
// the user-level claims authorize the brain call without any
// service-to-service token. Caller stuffs the bearer into ctx via
// WithBearerToken before calling TranslateRequest.

package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Resolver is the contract every file-resolution backend implements.
type Resolver interface {
	// PresignURL returns a short-lived presigned GET URL pointing to the
	// file in MinIO. The URL is good for ~15 minutes (server-side TTL);
	// callers should embed it directly into the outbound LLM request.
	// mediaType is what brain stored at finalize time (image/png etc).
	PresignURL(ctx context.Context, fileID string) (url, mediaType string, err error)

	// Fetch reads the bytes for a file_id. Bandwidth-heavy fallback for
	// providers that don't accept URLs.
	Fetch(ctx context.Context, fileID string) (bytes []byte, mediaType string, err error)
}

// HTTPResolver talks to a Brain instance over HTTP. The base URL is the
// brain externally-reachable endpoint (e.g. http://brain:8080). Token
// pulled from ctx via BearerFromContext.
type HTTPResolver struct {
	BrainBaseURL string
	HTTPClient   *http.Client
}

// NewHTTPResolver — convenience constructor with a sane default client.
func NewHTTPResolver(brainBaseURL string) *HTTPResolver {
	return &HTTPResolver{
		BrainBaseURL: brainBaseURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (r *HTTPResolver) PresignURL(ctx context.Context, fileID string) (string, string, error) {
	bearer, ok := BearerFromContext(ctx)
	if !ok || bearer == "" {
		return "", "", errors.New("files resolver: missing bearer token in ctx")
	}
	if r.BrainBaseURL == "" {
		return "", "", errors.New("files resolver: BrainBaseURL not configured")
	}
	url := r.BrainBaseURL + "/v1/files/" + fileID + "/presign-get"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("brain presign-get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("brain presign-get %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		URL       string `json:"url"`
		MediaType string `json:"media_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("brain presign-get decode: %w", err)
	}
	if out.URL == "" {
		return "", "", errors.New("brain presign-get: empty url")
	}
	return out.URL, out.MediaType, nil
}

func (r *HTTPResolver) Fetch(ctx context.Context, fileID string) ([]byte, string, error) {
	bearer, ok := BearerFromContext(ctx)
	if !ok || bearer == "" {
		return nil, "", errors.New("files resolver: missing bearer token in ctx")
	}
	if r.BrainBaseURL == "" {
		return nil, "", errors.New("files resolver: BrainBaseURL not configured")
	}
	url := r.BrainBaseURL + "/v1/files/" + fileID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("brain fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("brain fetch %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// ─── ctx plumbing ─────────────────────────────────────────

type bearerKey struct{}

// WithBearerToken stuffs the user JWT into ctx so adaptors can pull it
// out and pass to the resolver. model-relay MessagesHandler calls this before
// TranslateRequest.
func WithBearerToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerKey{}, token)
}

// BearerFromContext reverse of WithBearerToken. Returns ("", false)
// when the ctx never had one (e.g. unauthenticated path — shouldn't
// happen in real model-relay flow but possible in unit tests).
func BearerFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(bearerKey{}).(string)
	return v, ok && v != ""
}
