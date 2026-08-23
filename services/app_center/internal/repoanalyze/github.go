// GitHub REST API client for repo analysis. Bare net/http in the style
// of internal/rankings/client.go — the surface we need is four/five
// endpoints, far cheaper than a go-github dependency (this module's
// go.mod is standalone; new deps must be justified).
//
// Conditional GET: every method takes an etag ("" on first call) and
// returns the fresh ETag alongside the payload; a 304 short-circuits to
// ErrNotModified (pattern from biuapp/rss/fetcher.go:104-107). The
// release poller (tech plan §2.5) is the primary etag consumer; the
// one-shot Analyze flow passes "".

package repoanalyze

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.github.com"
	defaultTimeout = 10 * time.Second
	// Repo metadata / file contents are small; cap anyway so a hostile
	// or broken upstream can't stream us into OOM.
	maxBodyBytes = 4 << 20
)

var (
	ErrInvalidRepoURL = errors.New("repoanalyze: invalid repo url")
	// ErrRepoNotFound maps any upstream 404. Contents probes treat it as
	// "file absent"; the top-level Repo call treats it as "repo absent".
	ErrRepoNotFound   = errors.New("repoanalyze: repo not found")
	ErrNotModified    = errors.New("repoanalyze: not modified")
	ErrUpstreamFailed = errors.New("repoanalyze: github upstream failed")
	ErrUpstreamShape  = errors.New("repoanalyze: github response shape unexpected")
)

type Client struct {
	BaseURL string
	Token   string // optional; empty = anonymous (60 req/h)
	HTTP    *http.Client
}

func NewClient(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: defaultTimeout},
	}
}

// RepoInfo is the subset of GET /repos/{o}/{r} we consume.
type RepoInfo struct {
	FullName      string
	Description   string
	DefaultBranch string
	LicenseSPDX   string // "" when the repo declares no license; "NOASSERTION" when undetectable
	Stars         int
	Topics        []string
	HTMLURL       string
}

type rawRepo struct {
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch"`
	License       *struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`
	Stars   int      `json:"stargazers_count"`
	Topics  []string `json:"topics"`
	HTMLURL string   `json:"html_url"`
}

// Repo fetches repo metadata. 404 → ErrRepoNotFound.
func (c *Client) Repo(ctx context.Context, owner, name, etag string) (*RepoInfo, string, error) {
	body, newETag, err := c.get(ctx, "/repos/"+owner+"/"+name, etag)
	if err != nil {
		return nil, newETag, err
	}
	var raw rawRepo
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, newETag, fmt.Errorf("%w: %v", ErrUpstreamShape, err)
	}
	info := &RepoInfo{
		FullName:      raw.FullName,
		Description:   raw.Description,
		DefaultBranch: raw.DefaultBranch,
		Stars:         raw.Stars,
		Topics:        raw.Topics,
		HTMLURL:       raw.HTMLURL,
	}
	if raw.License != nil {
		info.LicenseSPDX = raw.License.SPDXID
	}
	if info.DefaultBranch == "" {
		info.DefaultBranch = "main"
	}
	return info, newETag, nil
}

// LatestRelease returns the latest release tag, or "" (nil error) when
// the repo has no releases — a 404 there means "no releases", not
// "no repo".
func (c *Client) LatestRelease(ctx context.Context, owner, name, etag string) (string, string, error) {
	body, newETag, err := c.get(ctx, "/repos/"+owner+"/"+name+"/releases/latest", etag)
	if errors.Is(err, ErrRepoNotFound) {
		return "", "", nil
	}
	if err != nil {
		return "", newETag, err
	}
	var raw struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", newETag, fmt.Errorf("%w: %v", ErrUpstreamShape, err)
	}
	return raw.TagName, newETag, nil
}

// Tag is one entry of the tags list (name + resolved commit sha).
type Tag struct {
	Name string
	SHA  string
}

// Tags lists repo tags (first page, newest first — enough for latest-ref
// resolution and release-tag → sha mapping).
func (c *Client) Tags(ctx context.Context, owner, name, etag string) ([]Tag, string, error) {
	body, newETag, err := c.get(ctx, "/repos/"+owner+"/"+name+"/tags?per_page=20", etag)
	if err != nil {
		return nil, newETag, err
	}
	var raw []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, newETag, fmt.Errorf("%w: %v", ErrUpstreamShape, err)
	}
	out := make([]Tag, 0, len(raw))
	for _, t := range raw {
		out = append(out, Tag{Name: t.Name, SHA: t.Commit.SHA})
	}
	return out, newETag, nil
}

// HeadSHA resolves a ref (branch / tag / sha) to a commit sha via
// GET /repos/{o}/{r}/commits/{ref}. Used when the repo has no release:
// we pin the default branch's HEAD as LatestSHA.
func (c *Client) HeadSHA(ctx context.Context, owner, name, ref string) (string, error) {
	body, _, err := c.get(ctx, "/repos/"+owner+"/"+name+"/commits/"+ref, "")
	if err != nil {
		return "", err
	}
	var raw struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUpstreamShape, err)
	}
	if raw.SHA == "" {
		return "", fmt.Errorf("%w: commits/%s returned no sha", ErrUpstreamShape, ref)
	}
	return raw.SHA, nil
}

// FileContent reads a single file via the contents API at the given ref
// ("" = default branch). ok=false when the path doesn't exist (404) —
// feature-file probing relies on this instead of error handling.
func (c *Client) FileContent(ctx context.Context, owner, name, path, ref string) (content []byte, ok bool, err error) {
	u := "/repos/" + owner + "/" + name + "/contents/" + path
	if ref != "" {
		u += "?ref=" + ref
	}
	body, _, err := c.get(ctx, u, "")
	if errors.Is(err, ErrRepoNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var raw struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrUpstreamShape, err)
	}
	if raw.Type != "file" {
		// Directory or symlink at this path — treat as absent for our
		// probing purposes.
		return nil, false, nil
	}
	if raw.Encoding != "base64" {
		return nil, false, fmt.Errorf("%w: contents/%s encoding %q", ErrUpstreamShape, path, raw.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(raw.Content, "\n", ""))
	if err != nil {
		return nil, false, fmt.Errorf("%w: contents/%s base64: %v", ErrUpstreamShape, path, err)
	}
	return decoded, true, nil
}

// get is the shared transport: auth header, conditional GET, status
// mapping, size-capped body read.
func (c *Client) get(ctx context.Context, path, etag string) (body []byte, newETag string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, "", fmt.Errorf("repoanalyze: build req: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "BiuMind/1.0 (+repoanalyze)")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUpstreamFailed, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return nil, etag, ErrNotModified
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", ErrRepoNotFound
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, "", fmt.Errorf("%w: status %d", ErrUpstreamFailed, resp.StatusCode)
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("%w: read: %v", ErrUpstreamFailed, err)
	}
	return body, resp.Header.Get("ETag"), nil
}
