package api

// toutiao_mp.go — 抖音小程序登录端点 (字节跳动小程序生态).
//
//	POST /v1/auth/toutiao/mp-login   { code, installation_id, device_name }
//
// 抖音 / 头条 / 西瓜共用一套 code2session API, 走 POST + JSON body.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/services/identity/internal/store"
)

type ToutiaoMPConfig struct {
	AppID      string
	AppSecret  string
	HTTPClient *http.Client
}

func (c *ToutiaoMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

var toutiaoCode2SessionURL = "https://developer.toutiao.com/api/apps/v2/jscode2session"

type toutiaoCode2SessionReq struct {
	AppID         string `json:"appid"`
	Secret        string `json:"secret"`
	Code          string `json:"code,omitempty"`
	AnonymousCode string `json:"anonymous_code,omitempty"`
}

type toutiaoCode2SessionResp struct {
	Err  int `json:"err_no"`
	Msg  string `json:"err_tips"`
	Data struct {
		OpenID     string `json:"openid"`
		UnionID    string `json:"unionid"`
		SessionKey string `json:"session_key"`
		AnonOpenID string `json:"anonymous_openid"`
	} `json:"data"`
}

func (c *ToutiaoMPConfig) code2session(ctx context.Context, code string) (*toutiaoCode2SessionResp, error) {
	if !c.Configured() {
		return nil, errors.New("toutiao_mp: not configured")
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	body, _ := json.Marshal(toutiaoCode2SessionReq{
		AppID: c.AppID, Secret: c.AppSecret, Code: code,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		toutiaoCode2SessionURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("toutiao code2session: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("toutiao code2session: read body: %w", err)
	}
	var r toutiaoCode2SessionResp
	if err := json.Unmarshal(respBody, &r); err != nil {
		return nil, fmt.Errorf("toutiao code2session: parse: %w", err)
	}
	if r.Err != 0 {
		return nil, fmt.Errorf("toutiao code2session: err_no=%d err_tips=%s", r.Err, r.Msg)
	}
	if r.Data.OpenID == "" {
		return nil, errors.New("toutiao code2session: empty openid")
	}
	return &r, nil
}

func (s *Server) handleToutiaoMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.ToutiaoMP == nil || !s.ToutiaoMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "toutiao_mp_not_configured", "")
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
	sess, err := s.ToutiaoMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("toutiao_mp.code2session", err)
		writeErr(w, http.StatusBadGateway, "toutiao_exchange_failed", err.Error())
		return
	}
	rawProfile, _ := json.Marshal(map[string]any{"openid": sess.Data.OpenID})
	prof := providerProfile{
		Provider:       store.ProviderToutiaoMP,
		ProviderUserID: sess.Data.OpenID,
		RawProfile:     rawProfile,
	}
	if sess.Data.UnionID != "" {
		uid := sess.Data.UnionID
		prof.UnionID = &uid
	}
	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("toutiao_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if req.DeviceName == "" {
		req.DeviceName = "toutiao-miniapp"
	}
	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
