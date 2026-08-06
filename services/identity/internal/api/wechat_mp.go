package api

// wechat_mp.go — 微信小程序登录端点.
//
//	POST /v1/auth/wechat/mp-login
//	  { "code": "<wx.login() js_code>",
//	    "installation_id": "<C 端 UUID>",
//	    "device_name": "wechat-miniapp" }
//	→ { access_token, refresh_token, expires_in_seconds, user }
//
// 流程:
//  1. code → 微信 code2session API → openid + unionid + session_key
//  2. resolveOrCreateUserByProvider 三段式合并 / 新建
//  3. issueTokensAndRespond 复用既有 token 颁发 (refresh_token rotate 等)

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

	"github.com/biumind/biumind/services/identity/internal/store"
)

// WechatMPConfig — 微信小程序应用配置. 由 main.go 注入到 Server.
// AppID / AppSecret 走 BYOK 信封加密路径 (生产由 KMS 注入到环境变量).
type WechatMPConfig struct {
	AppID     string
	AppSecret string
	// HTTPClient — 留出注入点给单元测试. nil 时用默认 10s 超时 client.
	HTTPClient *http.Client
}

// Configured 判断是否已配置 — 没配的话 handler 直接返 503.
// dev / test 环境允许不配, 不影响其他 endpoint.
func (c *WechatMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

// 微信官方端点. 测试用 sessionEndpoint 变量替换.
var wechatCode2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"

type wechatCode2SessionResp struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// code2session 调微信官方 API 用 js_code 换 openid + unionid.
// 失败一律返 error, 上层映射到 502 / 401.
func (c *WechatMPConfig) code2session(ctx context.Context, code string) (*wechatCode2SessionResp, error) {
	if !c.Configured() {
		return nil, errors.New("wechat_mp: not configured")
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	q := url.Values{}
	q.Set("appid", c.AppID)
	q.Set("secret", c.AppSecret)
	q.Set("js_code", code)
	q.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		wechatCode2SessionURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wechat code2session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wechat code2session: read body: %w", err)
	}
	var r wechatCode2SessionResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("wechat code2session: parse: %w (body=%s)", err, string(body))
	}
	if r.ErrCode != 0 {
		return nil, fmt.Errorf("wechat code2session: errcode=%d errmsg=%s", r.ErrCode, r.ErrMsg)
	}
	if r.OpenID == "" {
		return nil, errors.New("wechat code2session: empty openid")
	}
	return &r, nil
}

// handleWechatMPLogin 是路由 POST /v1/auth/wechat/mp-login 的入口.
func (s *Server) handleWechatMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.WechatMP == nil || !s.WechatMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "wechat_mp_not_configured", "")
		return
	}

	var req mpLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		writeErr(w, http.StatusBadRequest, "missing_code", "")
		return
	}

	sess, err := s.WechatMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("wechat_mp.code2session", err)
		writeErr(w, http.StatusBadGateway, "wechat_exchange_failed", err.Error())
		return
	}

	// raw profile 先存最小集合 (openid). 客户端后续再拉用户信息时由
	// 单独的 PATCH /v1/identity/me/providers/{id}/profile 端点写入扩展字段.
	rawProfile, _ := json.Marshal(map[string]any{
		"openid": sess.OpenID,
	})

	prof := providerProfile{
		Provider:       store.ProviderWechatMP,
		ProviderUserID: sess.OpenID,
		RawProfile:     rawProfile,
	}
	if sess.UnionID != "" {
		uid := sess.UnionID
		prof.UnionID = &uid
	}

	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("wechat_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}

	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
