// Package skillsync is the CLI's HTTP client for the runtime
// /v1/skills surface plus the bidirectional sync helpers (pull /
// push / diff) the `biu skill` subcommands drive.
//
// Wire format mirrors services/runtime/internal/api/skills_handlers
// 1:1; see docs/BiuMind-Skills-Design.md §6 for the proto these
// JSON keys come from. The struct names here are deliberately the
// same as the proto messages so a future Connect-Go migration is a
// straight swap of the transport layer.
package skillsync

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
	ErrConflict     = errors.New("identifier already taken")
)

// Skill — one wire-shaped row, decoded from the runtime response. We
// don't reuse the server-side skillsreg.Skill type because that
// would drag the runtime module into the CLI's go.mod (the two are
// already separate go.work modules and we'd like to keep it that
// way to avoid accidental kernel-only deps leaking into the CLI).
type Skill struct {
	ID            string                  `json:"id"`
	OrgID         string                  `json:"org_id"`
	OwnerID       string                  `json:"owner_id,omitempty"`
	Identifier    string                  `json:"identifier"`
	Name          string                  `json:"name"`
	Description   string                  `json:"description"`
	Source        string                  `json:"source"`
	Manifest      Manifest                `json:"manifest"`
	Content       string                  `json:"content"`
	ContentHash   string                  `json:"content_hash"`
	Resources     map[string]ResourceMeta `json:"resources,omitempty"`
	ZipFileSha256 string                  `json:"zip_file_sha256,omitempty"`
	Paths         []string                `json:"paths,omitempty"`
	Permissions   []string                `json:"permissions,omitempty"`
	Status        string                  `json:"status"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

type Manifest struct {
	Version    string            `json:"version,omitempty"`
	Author     ManifestAuthor    `json:"author,omitempty"`
	License    string            `json:"license,omitempty"`
	Repository string            `json:"repository,omitempty"`
	SourceURL  string            `json:"source_url,omitempty"`
	Extra      map[string]string `json:"extra,omitempty"`
}

type ManifestAuthor struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

type ResourceMeta struct {
	Sha256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	Inline    string `json:"inline,omitempty"`
}

// AgentSkill — toggle response shape.
type AgentSkill struct {
	AgentID   string    `json:"agent_id"`
	SkillID   string    `json:"skill_id"`
	IsEnabled bool      `json:"is_enabled"`
	Pinned    bool      `json:"pinned"`
	AddedAt   time.Time `json:"added_at"`
}

// Client wraps the runtime endpoint + Bearer token. Zero-value is
// invalid — use New.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ─── List ───────────────────────────────────────────────────

type ListOptions struct {
	Source  string // empty = no filter
	Status  string
	OwnerID string
}

func (c *Client) List(ctx context.Context, opt ListOptions) ([]Skill, error) {
	q := url.Values{}
	if opt.Source != "" {
		q.Set("source", opt.Source)
	}
	if opt.Status != "" {
		q.Set("status", opt.Status)
	}
	if opt.OwnerID != "" {
		q.Set("owner_id", opt.OwnerID)
	}
	endpoint := "/v1/skills"
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	var resp struct {
		Skills []Skill `json:"skills"`
	}
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Skills, nil
}

// ─── Get ────────────────────────────────────────────────────

func (c *Client) Get(ctx context.Context, id string) (*Skill, error) {
	var s Skill
	if err := c.do(ctx, http.MethodGet, "/v1/skills/"+url.PathEscape(id), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─── Install (inline) ───────────────────────────────────────

// InstallInlineRequest mirrors the server's installSkillReq for the
// inline source path.
type InstallInlineRequest struct {
	Identifier    string                  `json:"identifier"`
	Name          string                  `json:"name"`
	Description   string                  `json:"description"`
	Body          string                  `json:"body"`
	Manifest      Manifest                `json:"manifest"`
	Paths         []string                `json:"paths,omitempty"`
	Permissions   []string                `json:"permissions,omitempty"`
	Resources     map[string]ResourceMeta `json:"resources,omitempty"`
	TargetAgentID string                  `json:"target_agent_id,omitempty"`
	Pin           bool                    `json:"pin,omitempty"`
}

func (c *Client) InstallInline(ctx context.Context, req InstallInlineRequest) (*Skill, error) {
	var s Skill
	if err := c.do(ctx, http.MethodPost, "/v1/skills", req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// InstallURL asks the server to fetch a SKILL.md over HTTPS and
// register the resulting skill. PS2.3.
func (c *Client) InstallURL(ctx context.Context, fetchURL, targetAgentID string, pin bool) (*Skill, error) {
	body := map[string]any{
		"url":             fetchURL,
		"target_agent_id": targetAgentID,
		"pin":             pin,
	}
	var s Skill
	if err := c.do(ctx, http.MethodPost, "/v1/skills", body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// InstallZip uploads a base64-encoded .biuskill bundle. The server
// unzips, parses the SKILL.md, and inlines small resources. PS2.3.
func (c *Client) InstallZip(ctx context.Context, zipB64, targetAgentID string, pin bool) (*Skill, error) {
	body := map[string]any{
		"zip_b64":         zipB64,
		"target_agent_id": targetAgentID,
		"pin":             pin,
	}
	var s Skill
	if err := c.do(ctx, http.MethodPost, "/v1/skills", body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─── Update ─────────────────────────────────────────────────

type UpdateRequest struct {
	Description   *string                  `json:"description,omitempty"`
	Body          *string                  `json:"body,omitempty"`
	Manifest      *Manifest                `json:"manifest,omitempty"`
	Paths         *[]string                `json:"paths,omitempty"`
	Permissions   *[]string                `json:"permissions,omitempty"`
	Resources     *map[string]ResourceMeta `json:"resources,omitempty"`
	ZipFileSha256 *string                  `json:"zip_file_sha256,omitempty"`
}

func (c *Client) Update(ctx context.Context, id string, req UpdateRequest) (*Skill, error) {
	var s Skill
	if err := c.do(ctx, http.MethodPatch, "/v1/skills/"+url.PathEscape(id), req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─── Delete ─────────────────────────────────────────────────

func (c *Client) Delete(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/skills/"+url.PathEscape(id), nil, nil)
}

// ─── Toggle ─────────────────────────────────────────────────

type ToggleRequest struct {
	AgentID   string `json:"agent_id"`
	IsEnabled bool   `json:"is_enabled"`
	Pinned    bool   `json:"pinned"`
}

func (c *Client) Toggle(ctx context.Context, skillID string, req ToggleRequest) (*AgentSkill, error) {
	var as AgentSkill
	if err := c.do(ctx, http.MethodPost,
		"/v1/skills/"+url.PathEscape(skillID)+"/toggle", req, &as); err != nil {
		return nil, err
	}
	return &as, nil
}

// ─── transport ──────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if c.BaseURL == "" {
		return errors.New("skillsync: BaseURL not configured (set BIUMIND_RUNTIME_URL or pass --runtime-url)")
	}
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
			return fmt.Errorf("%w: %s", ErrUnauthorized, snippet(raw))
		case http.StatusNotFound:
			return ErrNotFound
		case http.StatusConflict:
			return fmt.Errorf("%w: %s", ErrConflict, snippet(raw))
		default:
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(raw))
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func snippet(raw []byte) string {
	if len(raw) > 256 {
		raw = raw[:256]
	}
	return strings.TrimSpace(string(raw))
}
