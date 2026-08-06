package api

// qq_mp.go — QQ 小程序登录端点.
//
//	POST /v1/auth/qq/mp-login   { code, installation_id, device_name }
//
// QQ 小程序与微信同内核, code2session 协议一致, 仅 host 不同.

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

type QQMPConfig struct {
	AppID      string
	AppSecret  string
	HTTPClient *http.Client
}

func (c *QQMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

var qqCode2SessionURL = "https://api.q.qq.com/sns/jscode2session"

type qqCode2SessionResp struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

func (c *QQMPConfig) code2session(ctx context.Context, code string) (*qqCode2SessionResp, error) {
	if !c.Configured() {
		return nil, errors.New("qq_mp: not configured")
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
		qqCode2SessionURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qq code2session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qq code2session: read body: %w", err)
	}
	var r qqCode2SessionResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("qq code2session: parse: %w", err)
	}
	if r.ErrCode != 0 {
		return nil, fmt.Errorf("qq code2session: errcode=%d errmsg=%s", r.ErrCode, r.ErrMsg)
	}
	if r.OpenID == "" {
		return nil, errors.New("qq code2session: empty openid")
	}
	return &r, nil
}

func (s *Server) handleQQMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.QQMP == nil || !s.QQMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "qq_mp_not_configured", "")
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
	sess, err := s.QQMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("qq_mp.code2session", err)
		writeErr(w, http.StatusBadGateway, "qq_exchange_failed", err.Error())
		return
	}
	rawProfile, _ := json.Marshal(map[string]any{"openid": sess.OpenID})
	prof := providerProfile{
		Provider:       store.ProviderQQMP,
		ProviderUserID: sess.OpenID,
		RawProfile:     rawProfile,
	}
	// 注: QQ 小程序的 unionid 与微信 unionid 不互通; 不做生态合并.
	if sess.UnionID != "" {
		uid := sess.UnionID
		prof.UnionID = &uid
	}
	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("qq_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if req.DeviceName == "" {
		req.DeviceName = "qq-miniapp"
	}
	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
