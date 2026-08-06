package api

// lark_mp.go — 飞书小程序登录端点.
//
//	POST /v1/auth/lark/mp-login   { code, installation_id, device_name }
//
// 飞书走 OAuth: code → app_access_token + user_access_token + open_id.
//
//   1. POST /open-apis/auth/v3/app_access_token/internal { app_id, app_secret }
//      → app_access_token
//   2. GET /open-apis/authen/v1/access_token?grant_type=authorization_code&code=...
//      Header: Authorization: Bearer <app_access_token>
//      → { open_id, union_id, user_id, ... }
//
// 当前实现完整路径但只走最小调用 (拿到 open_id 即可); 后续按需扩 user info.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/services/identity/internal/store"
)

type LarkMPConfig struct {
	AppID      string
	AppSecret  string
	HTTPClient *http.Client

	// app_access_token 缓存 — 失效前复用. mu 保护 cache.
	mu               sync.Mutex
	cachedAppToken   string
	cachedAppTokenAt time.Time
}

func (c *LarkMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

const (
	larkAppAccessTokenURL = "https://open.feishu.cn/open-apis/auth/v3/app_access_token/internal"
	larkAccessTokenURL    = "https://open.feishu.cn/open-apis/authen/v1/access_token"
)

type larkAppAccessTokenResp struct {
	Code           int    `json:"code"`
	Msg            string `json:"msg"`
	AppAccessToken string `json:"app_access_token"`
	Expire         int    `json:"expire"`
}

type larkAccessTokenResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		AccessToken string `json:"access_token"`
		OpenID      string `json:"open_id"`
		UnionID     string `json:"union_id"`
		UserID      string `json:"user_id"`
		Name        string `json:"name"`
		AvatarURL   string `json:"avatar_url"`
	} `json:"data"`
}

func (c *LarkMPConfig) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *LarkMPConfig) appAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 飞书 app_access_token 默认 2h, 留 5 min buffer
	if c.cachedAppToken != "" && time.Since(c.cachedAppTokenAt) < 110*time.Minute {
		return c.cachedAppToken, nil
	}
	body, _ := json.Marshal(map[string]string{
		"app_id":     c.AppID,
		"app_secret": c.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		larkAppAccessTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("lark app_access_token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r larkAppAccessTokenResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("lark app_access_token: parse: %w", err)
	}
	if r.Code != 0 || r.AppAccessToken == "" {
		return "", fmt.Errorf("lark app_access_token: code=%d msg=%s", r.Code, r.Msg)
	}
	c.cachedAppToken = r.AppAccessToken
	c.cachedAppTokenAt = time.Now()
	return c.cachedAppToken, nil
}

func (c *LarkMPConfig) code2session(ctx context.Context, code string) (*larkAccessTokenResp, error) {
	if !c.Configured() {
		return nil, errors.New("lark_mp: not configured")
	}
	appTok, err := c.appAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{
		"grant_type": "authorization_code",
		"code":       code,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		larkAccessTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+appTok)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("lark access_token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r larkAccessTokenResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("lark access_token: parse: %w", err)
	}
	if r.Code != 0 || r.Data.OpenID == "" {
		return nil, fmt.Errorf("lark access_token: code=%d msg=%s", r.Code, r.Msg)
	}
	return &r, nil
}

func (s *Server) handleLarkMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.LarkMP == nil || !s.LarkMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "lark_mp_not_configured", "")
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
	sess, err := s.LarkMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("lark_mp.code2session", err)
		writeErr(w, http.StatusBadGateway, "lark_exchange_failed", err.Error())
		return
	}
	rawProfile, _ := json.Marshal(map[string]any{
		"open_id":    sess.Data.OpenID,
		"nickname":   sess.Data.Name,
		"avatar_url": sess.Data.AvatarURL,
	})
	prof := providerProfile{
		Provider:       store.ProviderLarkMP,
		ProviderUserID: sess.Data.OpenID,
		RawProfile:     rawProfile,
	}
	if sess.Data.UnionID != "" {
		uid := sess.Data.UnionID
		prof.UnionID = &uid
	}
	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("lark_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if req.DeviceName == "" {
		req.DeviceName = "lark-miniapp"
	}
	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
