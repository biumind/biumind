package api

// baidu_mp.go — 百度智能小程序登录端点.
//
//	POST /v1/auth/baidu/mp-login   { code, installation_id, device_name }
//
// 百度走 POST form-urlencoded.

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

type BaiduMPConfig struct {
	AppID      string
	AppSecret  string
	HTTPClient *http.Client
}

func (c *BaiduMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

var baiduCode2SessionURL = "https://spapi.baidu.com/oauth/jscode2sessionkey"

type baiduCode2SessionResp struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	Errno      int    `json:"errno"`
	ErrMsg     string `json:"errmsg"`
}

func (c *BaiduMPConfig) code2session(ctx context.Context, code string) (*baiduCode2SessionResp, error) {
	if !c.Configured() {
		return nil, errors.New("baidu_mp: not configured")
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", c.AppID)
	form.Set("sk", c.AppSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baiduCode2SessionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("baidu code2session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("baidu code2session: read body: %w", err)
	}
	var r baiduCode2SessionResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("baidu code2session: parse: %w", err)
	}
	if r.Errno != 0 {
		return nil, fmt.Errorf("baidu code2session: errno=%d errmsg=%s", r.Errno, r.ErrMsg)
	}
	if r.OpenID == "" {
		return nil, errors.New("baidu code2session: empty openid")
	}
	return &r, nil
}

func (s *Server) handleBaiduMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.BaiduMP == nil || !s.BaiduMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "baidu_mp_not_configured", "")
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
	sess, err := s.BaiduMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("baidu_mp.code2session", err)
		writeErr(w, http.StatusBadGateway, "baidu_exchange_failed", err.Error())
		return
	}
	rawProfile, _ := json.Marshal(map[string]any{"openid": sess.OpenID})
	prof := providerProfile{
		Provider:       store.ProviderBaiduMP,
		ProviderUserID: sess.OpenID,
		RawProfile:     rawProfile,
	}
	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("baidu_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if req.DeviceName == "" {
		req.DeviceName = "baidu-miniapp"
	}
	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
