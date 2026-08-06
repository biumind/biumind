// identity_byok.go — brain → identity service-to-service BYOK client.
//
// P3: brain 不再持有用户 key 明文 (chat.providers.key_vaults_encrypted 已删).
// 需要用户上游凭据的两处 reader 改向 identity 取:
//
//   * providers.refresh — 拉上游 /models 列表要鉴权 (用户 key + base_url)
//   * agentplane.ResolveBYOKCreds — chat 模式进程内 ChatRunner 解析 BYOK
//     (agent/task 模式 P4 起改委托 user JWT, 不再 brain 预解析 key 投递)
//
// 调 identity 内部 endpoint (同 model-relay 的 byok resolver):
//
//	GET /v1/internal/byok/{user_id}/{provider}   返 {api_key, base_url, protocol, config}
//
// 鉴权: 共享 service bearer (env IDENTITY_INTERNAL_TOKEN, identity/cmd/main.go:107).
// 这些 endpoint 返明文 key, 必须靠 NetworkPolicy 限制只有受信 Pod 触达.
//
// 失败语义: 404 (用户没配该 provider 的 key / status!=valid) → ErrIdentityKeyNotFound,
// 调用方据此跳过 BYOK 走平台兜底. 其他错误 (网络/5xx) → 透传 err 由调用方 log.
// client 未配置 (dev 没配 IDENTITY_URL) → Get 直接返 ErrIdentityKeyNotFound, 不报错.

package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrIdentityKeyNotFound — identity 没有该 (user, provider) 的有效 key.
// 与 brain 本地 ErrNotFound 区分: 后者是 brain providers 行查不到.
var ErrIdentityKeyNotFound = errors.New("identity byok: no valid key for user/provider")

// IdentityBYOKKey — identity 返回的用户上游凭据 (明文).
type IdentityBYOKKey struct {
	APIKey   string // 明文; 调用方用完即弃, 不落 brain 存储
	BaseURL  string // custom/代理 endpoint; 标准 provider 空 (走默认)
	Protocol string // openai_compat/anthropic/google/...; brain 暂不消费 (relay 用)
}

// IdentityBYOKClient 调 identity /v1/internal/byok/*. nil-safe: 未配置时所有
// 方法返 ErrIdentityKeyNotFound, 让调用方优雅降级 (brain 在 dev / 未配
// IDENTITY_URL 时不应因 BYOK 查询崩溃).
type IdentityBYOKClient struct {
	BaseURL string // 例 "http://identity:7004", 末尾不带 /
	Token   string // IDENTITY_INTERNAL_TOKEN
	HTTP    *http.Client
}

// NewIdentityBYOKClient 构造. 空 baseURL → 返回的 client Get 直接返 not-found
// (让 main.go 对未配 IDENTITY_URL 的 dev 友好, 不阻塞 refresh / ResolveBYOKCreds).
func NewIdentityBYOKClient(baseURL, token string) *IdentityBYOKClient {
	return &IdentityBYOKClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 3 * time.Second},
	}
}

// Get 取 (userID, provider) 的明文 key + endpoint. provider 是 identity 枚举
// (anthropic/openai/google/...); custom slug 会命中 identity 的 'custom' 记录
// (若用户仅一个 custom). 404 → ErrIdentityKeyNotFound.
func (c *IdentityBYOKClient) Get(ctx context.Context, userID uuid.UUID, provider string) (*IdentityBYOKKey, error) {
	if c == nil || c.BaseURL == "" || provider == "" || userID == uuid.Nil {
		return nil, ErrIdentityKeyNotFound
	}
	url := fmt.Sprintf("%s/v1/internal/byok/%s/%s", c.BaseURL, userID, provider)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity byok: http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrIdentityKeyNotFound
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("identity byok: http %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
		Protocol string `json:"protocol"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("identity byok: decode: %w", err)
	}
	if out.APIKey == "" {
		return nil, ErrIdentityKeyNotFound
	}
	return &IdentityBYOKKey{APIKey: out.APIKey, BaseURL: out.BaseURL, Protocol: out.Protocol}, nil
}
