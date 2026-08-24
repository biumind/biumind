// Server-side reporting for repo-app builds/updates (M2).
//
// When the desktop client drives an update (POST /v1/apps/installs/{id}/redeploy
// → `biu repo-app update <slug> --ref <ref> --install-id .. --build-id ..`),
// the CLI posts the outcome back to app_center so the server can flip the
// repo_builds row to live/failed and record the installed sha
// (TechPlan §3.2 client.go + §5.2). Pattern mirrors
// internal/skillsync/client.go (Bearer auth + sentinel errors + httptest
// tests); token resolution happens caller-side via wiring.TokenProviderFor.

package repoapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
)

// Build statuses accepted by the app_center complete endpoint.
const (
	BuildStatusLive   = "live"
	BuildStatusFailed = "failed"
)

// BuildResult is the request body of CompleteBuild. LogRef is reserved
// for a future log-upload handle and stays empty for now.
type BuildResult struct {
	Status string `json:"status"` // BuildStatusLive | BuildStatusFailed
	SHA    string `json:"sha"`    // HEAD sha after the update ("" when the fetch never ran)
	LogRef string `json:"log_ref"`
}

// ReportClient wraps the app_center endpoint + Bearer token. Zero-value
// is invalid — use NewReportClient.
type ReportClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewReportClient(baseURL, token string) *ReportClient {
	return &ReportClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// CompleteBuild POSTs the build outcome to
// /v1/apps/installs/{installID}/builds/{buildID}/complete.
func (c *ReportClient) CompleteBuild(ctx context.Context, installID, buildID string, result BuildResult) error {
	if c.BaseURL == "" {
		return errors.New("repoapp: BaseURL not configured (pass --report-url or set [model-relay].endpoint)")
	}
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	path := "/v1/apps/installs/" + url.PathEscape(installID) +
		"/builds/" + url.PathEscape(buildID) + "/complete"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: %s", ErrUnauthorized, reportSnippet(raw))
		case http.StatusNotFound:
			return ErrNotFound
		case http.StatusConflict:
			return fmt.Errorf("%w: %s", ErrConflict, reportSnippet(raw))
		default:
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, reportSnippet(raw))
		}
	}
	return nil
}

func reportSnippet(raw []byte) string {
	if len(raw) > 256 {
		raw = raw[:256]
	}
	return strings.TrimSpace(string(raw))
}
