// identity_byok.go — daemon 调 identity 取 client-side BYOK 凭据 (user JWT).
//
// 替代 B2 的 loopback credStore (key 经 Flutter 推本机内存). 现状: client-side
// key 统一加密存 identity, daemon 命中 client-side work (WorkPayload 带
// ClientSideRecordID) 时用 work.UserBearer (brain 透传的 user JWT) 调 identity
// 新端点 GET /v1/identity/me/api-keys/{id}/credentials 取明文 key, 本机直连
// 上游 —— 不经 model-relay.
//
// 仅 is_client_side=true 行可取 (identity store 过滤 + 端点 owner-scoped).
// 取失败 (record 不存在 / revoked / 网络断) → 调用方落 relay fallback.

package agentplane

import (
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

// ErrIdentityCredentialNotFound — identity 端点返 404 (record 不存在 / 非
// client-side / 跨 user / revoked). 调用方静默落 relay fallback (非错误).
var ErrIdentityCredentialNotFound = errors.New("agentplane: identity client-side credential not found")

// IdentityCredential — identity 返的 client-side 凭据明文.
type IdentityCredential struct {
	Key      string
	BaseURL  string
	Protocol string
}

// IdentityBYOKClient — daemon 调 identity 取 client-side key. 用 user JWT
// (work.UserBearer) 鉴权, 非 HUB_INTERNAL_TOKEN (那是 relay 调 internalapi 用).
type IdentityBYOKClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewIdentityBYOKClient(baseURL string) *IdentityBYOKClient {
	return &IdentityBYOKClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// GetCredential — 按 recordID 取 client-side 凭据明文. userJWT 为 brain 透传的
// 委托 user access_token (work.UserBearer). 返 ErrIdentityCredentialNotFound
// (404, 静默 fallback) 或 HTTP/network err (调用方 log).
func (c *IdentityBYOKClient) GetCredential(ctx context.Context, userJWT, recordID string) (*IdentityCredential, error) {
	if c.BaseURL == "" {
		return nil, errors.New("agentplane: identity URL not configured")
	}
	if userJWT == "" || recordID == "" {
		return nil, ErrIdentityCredentialNotFound
	}
	u := fmt.Sprintf("%s/v1/identity/me/api-keys/%s/credentials",
		c.BaseURL, url.PathEscape(recordID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+userJWT)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrIdentityCredentialNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("agentplane: identity 401 (user JWT 无效或过期)")
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentplane: identity http %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var body struct {
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
		Protocol string `json:"protocol"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("agentplane: decode identity resp: %w", err)
	}
	if body.APIKey == "" {
		return nil, ErrIdentityCredentialNotFound
	}
	return &IdentityCredential{
		Key:      body.APIKey,
		BaseURL:  body.BaseURL,
		Protocol: body.Protocol,
	}, nil
}
