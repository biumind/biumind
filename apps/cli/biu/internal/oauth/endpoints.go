// 端点推导（方案 D4）：identity 与 model-relay 同 origin（site nginx
// 网关），所以 [model-relay].endpoint 的 scheme://host 就是 OAuth
// 授权服务器的 base。authorize / token / revoke 三个端点由此推导，
// 默认安装零新增配置；自部署（identity 与 relay 不同 origin）用
// [auth] 段或 BIU_OAUTH_* env 显式覆盖。

package oauth

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// DefaultClientID 是 identity 侧预注册的 public client（方案 D2，
// 无 secret、强制 PKCE）。
const DefaultClientID = "biu-cli"

// DeriveEndpoints 从 relay endpoint（如 https://biumind.xxlab.tech 或
// http://localhost:7001/v1）推导 OAuth 端点：只取 scheme://host，
// 拼上固定的 /oauth/* 路径。endpoint 为空或不是合法 http(s) URL 时
// 报错——调用方此时才应回落"[auth] section incomplete"类提示。
func DeriveEndpoints(relayEndpoint string) (Config, error) {
	relayEndpoint = strings.TrimSpace(relayEndpoint)
	if relayEndpoint == "" {
		return Config{}, fmt.Errorf("oauth: cannot derive endpoints from empty relay endpoint")
	}
	u, err := url.Parse(relayEndpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return Config{}, fmt.Errorf("oauth: cannot derive endpoints from relay endpoint %q — expect an http(s) URL", relayEndpoint)
	}
	base := u.Scheme + "://" + u.Host
	return Config{
		AuthorizeURL: base + "/oauth/authorize",
		TokenURL:     base + "/oauth/token",
		RevokeURL:    base + "/oauth/revoke",
		ClientID:     DefaultClientID,
	}, nil
}

// MergeOAuthConfig 按优先级合并 OAuth 配置：[auth] TOML 段 >
// BIU_OAUTH_* env > 推导值。toml/env 任一字段为空即落下一级；
// derived 为 DeriveEndpoints 的结果（可为零值）。返回值的 ClientID
// 保底为 DefaultClientID。
func MergeOAuthConfig(toml Config, env Config, derived Config) Config {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	out := Config{
		AuthorizeURL:      pick(toml.AuthorizeURL, env.AuthorizeURL, derived.AuthorizeURL),
		TokenURL:          pick(toml.TokenURL, env.TokenURL, derived.TokenURL),
		RevokeURL:         pick(toml.RevokeURL, env.RevokeURL, derived.RevokeURL),
		ClientID:          pick(toml.ClientID, env.ClientID, derived.ClientID, DefaultClientID),
		CallbackPort:      toml.CallbackPort,
		ManualRedirectURL: pick(toml.ManualRedirectURL, env.ManualRedirectURL),
	}
	if len(toml.Scopes) > 0 {
		out.Scopes = toml.Scopes
	}
	return out
}

// ConfigFromSources 是 C1 的统一入口：读 BIU_OAUTH_* env，从
// relayEndpoint 推导默认值，与 [auth] TOML 段合并。返回 error 仅在
// 合并后仍缺 authorize_url/token_url/client_id 时——通常意味着
// relay endpoint 非法导致推导失败，报错里带原始原因。
func ConfigFromSources(authToml Config, relayEndpoint string) (Config, error) {
	env := Config{
		AuthorizeURL:      os.Getenv("BIU_OAUTH_AUTHORIZE_URL"),
		TokenURL:          os.Getenv("BIU_OAUTH_TOKEN_URL"),
		RevokeURL:         os.Getenv("BIU_OAUTH_REVOKE_URL"),
		ClientID:          os.Getenv("BIU_OAUTH_CLIENT_ID"),
		ManualRedirectURL: os.Getenv("BIU_OAUTH_MANUAL_REDIRECT"),
	}
	derived, derr := DeriveEndpoints(relayEndpoint)
	out := MergeOAuthConfig(authToml, env, derived)
	if len(out.Scopes) == 0 {
		out.Scopes = []string{"openid", "profile"}
	}
	if out.AuthorizeURL == "" || out.TokenURL == "" || out.ClientID == "" {
		if derr != nil {
			return out, derr
		}
		return out, fmt.Errorf("oauth: [auth] section incomplete: authorize_url, token_url, client_id all required")
	}
	return out, nil
}
