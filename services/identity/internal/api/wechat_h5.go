package api

// wechat_h5.go — 微信网页授权 (OAuth 2.0 Authorization Code Flow).
//
// 区别于 wechat_mp.go (小程序登录, code 来自 wx.login):
//   - 这里 code 来自浏览器 redirect URL, 走完整 OAuth 三段:
//     /authorize → 跳微信 → /callback ← 微信带 code 回
//   - 凭据用 *Web* 公众号 / 开放平台 appid (与小程序 appid 不同;
//     unionid 在同一开放平台主体下仍能合并)
//
// 端点:
//   GET /v1/auth/wechat/h5-authorize?return=/path&scope=snsapi_userinfo
//        ↓ 302
//        https://open.weixin.qq.com/connect/oauth2/authorize?...&state=
//
//   GET /v1/auth/wechat/h5-callback?code=&state=
//        ↓ 302
//        <FRONTEND_BASE>/pages/me/oauth-return#access_token=...&refresh_token=...&expires_in=...&return=...
//
// 用 fragment (#) 传 token: 不进 server-side referer / access log; 浏览器
// history 即使被同步也只是 fragment, 主流恶意脚本拿不到. 比 query string
// 安全, 比 HttpOnly cookie 简单 (不破坏既有 token_manager 跨平台模型).

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/biumind/biumind/services/identity/internal/token"
)

// WechatWebConfig — 微信网页授权 (公众号 / 开放平台 网站应用) 配置.
// 与 WechatMPConfig 互不依赖; 但同一 open-platform 主体下 unionid 互通.
type WechatWebConfig struct {
	AppID     string
	AppSecret string
	// FrontendBaseURL — 前端 H5 部署的 URL, callback 完成后 302 redirect 到
	// `<base>/pages/me/oauth-return#...`. 例: https://mp.biumind.cn
	FrontendBaseURL string
	// AuthorizeRedirect — 默认与 FrontendBaseURL 拼出 callback URL;
	// 该 URL 必须在微信公众号后台"授权回调域名"白名单中. 留空时由 caller
	// 用 BuildCallbackURL 在 request 时基于 r.Host 拼.
	CallbackURL string
	HTTPClient  *http.Client
	// StateTTL — oauth_states TTL, 默认 5min.
	StateTTL time.Duration
}

func (c *WechatWebConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != "" && c.FrontendBaseURL != ""
}

func (c *WechatWebConfig) ttl() time.Duration {
	if c.StateTTL > 0 {
		return c.StateTTL
	}
	return 5 * time.Minute
}

func (c *WechatWebConfig) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// 微信 OAuth 端点
const (
	wechatWebAuthorizeURL   = "https://open.weixin.qq.com/connect/oauth2/authorize"
	wechatWebAccessTokenURL = "https://api.weixin.qq.com/sns/oauth2/access_token"
	wechatWebUserInfoURL    = "https://api.weixin.qq.com/sns/userinfo"
)

// generateState — 32 字节 random base64url (无 padding), 长度 ~43 字符.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// safeReturnURL — 只允许相对路径或 FrontendBaseURL 同源, 避免 open redirect.
func safeReturnURL(raw, frontendBase string) string {
	if raw == "" {
		return "/"
	}
	// 相对路径直接通过
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw
	}
	// 绝对路径必须同源
	u, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	base, err := url.Parse(frontendBase)
	if err != nil {
		return "/"
	}
	if u.Scheme == base.Scheme && u.Host == base.Host {
		return u.Path + "?" + u.RawQuery + "#" + u.Fragment
	}
	// 不同源 — 拒, 跳首页
	return "/"
}

// ── /authorize: 生成 state + 跳微信 ──────────────────────────────

func (s *Server) handleWechatH5Authorize(w http.ResponseWriter, r *http.Request) {
	if s.WechatWeb == nil || !s.WechatWeb.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "wechat_web_not_configured", "")
		return
	}
	returnURL := safeReturnURL(r.URL.Query().Get("return"), s.WechatWeb.FrontendBaseURL)
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "snsapi_userinfo" // 拿头像/昵称; snsapi_base 仅 openid
	}
	state, err := generateState()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := s.Store.CreateOAuthState(
		r.Context(), state, "wechat_web", returnURL, s.WechatWeb.ttl(),
	); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	callback := s.wechatCallbackURL(r)
	q := url.Values{}
	q.Set("appid", s.WechatWeb.AppID)
	q.Set("redirect_uri", callback)
	q.Set("response_type", "code")
	q.Set("scope", scope)
	q.Set("state", state)
	// 微信特殊: fragment 必须是 #wechat_redirect 否则不识别
	target := wechatWebAuthorizeURL + "?" + q.Encode() + "#wechat_redirect"
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) wechatCallbackURL(r *http.Request) string {
	if s.WechatWeb.CallbackURL != "" {
		return s.WechatWeb.CallbackURL
	}
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if xfp := r.Header.Get("X-Forwarded-Proto"); xfp != "" {
		scheme = xfp
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + "/v1/auth/wechat/h5-callback"
}

// ── /callback: 校验 state + 换 token + fragment redirect ─────────

type wechatWebAccessTokenResp struct {
	AccessToken    string `json:"access_token"`
	ExpiresIn      int    `json:"expires_in"`
	RefreshToken   string `json:"refresh_token"`
	OpenID         string `json:"openid"`
	Scope          string `json:"scope"`
	UnionID        string `json:"unionid"`
	ErrCode        int    `json:"errcode"`
	ErrMsg         string `json:"errmsg"`
}

type wechatWebUserInfoResp struct {
	OpenID    string `json:"openid"`
	NickName  string `json:"nickname"`
	HeadImg   string `json:"headimgurl"`
	UnionID   string `json:"unionid"`
	ErrCode   int    `json:"errcode"`
	ErrMsg    string `json:"errmsg"`
}

func (c *WechatWebConfig) exchangeCode(ctx context.Context, code string) (*wechatWebAccessTokenResp, error) {
	q := url.Values{}
	q.Set("appid", c.AppID)
	q.Set("secret", c.AppSecret)
	q.Set("code", code)
	q.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		wechatWebAccessTokenURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("wechat oauth2 access_token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r wechatWebAccessTokenResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse: %w (body=%s)", err, string(body))
	}
	if r.ErrCode != 0 {
		return nil, fmt.Errorf("errcode=%d errmsg=%s", r.ErrCode, r.ErrMsg)
	}
	if r.OpenID == "" {
		return nil, errors.New("empty openid")
	}
	return &r, nil
}

// fetchUserInfo — snsapi_userinfo scope 时调; snsapi_base 时跳过.
// 失败不致命 — openid 已有, 用占位 nickname 也能登录.
func (c *WechatWebConfig) fetchUserInfo(ctx context.Context, accessToken, openid string) *wechatWebUserInfoResp {
	q := url.Values{}
	q.Set("access_token", accessToken)
	q.Set("openid", openid)
	q.Set("lang", "zh_CN")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		wechatWebUserInfoURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r wechatWebUserInfoResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}
	if r.ErrCode != 0 {
		return nil
	}
	return &r
}

func (s *Server) handleWechatH5Callback(w http.ResponseWriter, r *http.Request) {
	if s.WechatWeb == nil || !s.WechatWeb.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "wechat_web_not_configured", "")
		return
	}
	q := r.URL.Query()
	code := q.Get("code")
	state := q.Get("state")
	// 用户在微信里点"取消" — 微信带 code=空 回来. 直接跳回前端首页.
	if code == "" {
		s.h5RedirectError(w, r, "/", "user_cancelled")
		return
	}
	if state == "" {
		s.h5RedirectError(w, r, "/", "missing_state")
		return
	}
	st, err := s.Store.ConsumeOAuthState(r.Context(), state)
	if err != nil {
		s.h5RedirectError(w, r, "/", "invalid_state")
		return
	}
	if st.Provider != "wechat_web" {
		s.h5RedirectError(w, r, st.ReturnURL, "provider_mismatch")
		return
	}

	tok, err := s.WechatWeb.exchangeCode(r.Context(), code)
	if err != nil {
		s.logErr("wechat_h5.exchangeCode", err)
		s.h5RedirectError(w, r, st.ReturnURL, "exchange_failed")
		return
	}

	// 拉用户信息 (best-effort) — 拿到 nickname / avatar 给 me 页面用
	info := s.WechatWeb.fetchUserInfo(r.Context(), tok.AccessToken, tok.OpenID)

	rawProfile := map[string]any{"openid": tok.OpenID}
	if info != nil {
		rawProfile["nickname"] = info.NickName
		rawProfile["avatar_url"] = info.HeadImg
	}
	rawJSON, _ := json.Marshal(rawProfile)

	prof := providerProfile{
		// wechat_web 与 wechat_mp 在 unionid 合并里同属微信生态:
		// store.IsWechatEcosystem 已包含 wechat_open. 用 wechat_open 让两端
		// 共享身份 — 同一 open-platform 主体下小程序与公众号合并到同一 user.
		Provider:       store.ProviderWechatOpen,
		ProviderUserID: tok.OpenID,
		RawProfile:     rawJSON,
	}
	if tok.UnionID != "" {
		uid := tok.UnionID
		prof.UnionID = &uid
	}

	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("wechat_h5.resolveOrCreate", err)
		s.h5RedirectError(w, r, st.ReturnURL, "internal")
		return
	}

	// 颁 BiuMind JWT (与 mp_common 同路径 — refresh_token rotate / device 模型).
	full, hash, err := token.Generate(token.RefreshTokenPrefix)
	if err != nil {
		s.logErr("wechat_h5.signRefreshToken", err)
		s.h5RedirectError(w, r, st.ReturnURL, "internal")
		return
	}
	// installation_id: H5 没有稳定的 install id (cookie 也是浏览器级别),
	// 用空字符串走"老 client 兼容路径": partial unique index 不约束空 install,
	// 每次 H5 OAuth 都新建一行 refresh_token (不堆积同设备 — 因为 H5 多标签
	// 各算一个 device 也合理).
	sid, err := s.Store.CreateOrRotateRefreshToken(
		r.Context(), u.ID, "", hash, "wechat-h5",
		s.RefreshTTL, s.refreshAbsoluteTTL(),
	)
	if err != nil {
		s.logErr("wechat_h5.CreateRefresh", err)
		s.h5RedirectError(w, r, st.ReturnURL, "internal")
		return
	}
	access, err := s.Signer.Sign(buildClaims(u, sid.String()))
	if err != nil {
		s.logErr("wechat_h5.Sign", err)
		s.h5RedirectError(w, r, st.ReturnURL, "internal")
		return
	}

	// fragment 携带 token: 浏览器不会发到 server, 只在 client 内被 SPA 读取.
	frag := url.Values{}
	frag.Set("access_token", access)
	frag.Set("refresh_token", full)
	frag.Set("expires_in", fmt.Sprintf("%d", int(s.AccessTTL.Seconds())))
	frag.Set("return", st.ReturnURL)

	target := strings.TrimRight(s.WechatWeb.FrontendBaseURL, "/") +
		"/pages/me/oauth-return#" + frag.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}

// h5RedirectError — 失败统一跳前端 oauth-return, 让前端 SPA 显示错误.
func (s *Server) h5RedirectError(w http.ResponseWriter, r *http.Request, returnURL, code string) {
	frag := url.Values{}
	frag.Set("error", code)
	frag.Set("return", returnURL)
	target := strings.TrimRight(s.WechatWeb.FrontendBaseURL, "/") +
		"/pages/me/oauth-return#" + frag.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}

