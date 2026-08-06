package api

// kuaishou_mp.go — 快手小程序登录端点.
//
//	POST /v1/auth/kuaishou/mp-login   { code, installation_id, device_name }

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

type KuaishouMPConfig struct {
	AppID      string
	AppSecret  string
	HTTPClient *http.Client
}

func (c *KuaishouMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

var kuaishouCode2SessionURL = "https://open.kuaishou.com/oauth2/mp/code2session"

type kuaishouCode2SessionResp struct {
	Result     int    `json:"result"`
	ErrorMsg   string `json:"error_msg"`
	OpenID     string `json:"open_id"`
	SessionKey string `json:"session_key"`
}

func (c *KuaishouMPConfig) code2session(ctx context.Context, code string) (*kuaishouCode2SessionResp, error) {
	if !c.Configured() {
		return nil, errors.New("kuaishou_mp: not configured")
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	q := url.Values{}
	q.Set("app_id", c.AppID)
	q.Set("app_secret", c.AppSecret)
	q.Set("js_code", code)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		kuaishouCode2SessionURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kuaishou code2session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kuaishou code2session: read body: %w", err)
	}
	var r kuaishouCode2SessionResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("kuaishou code2session: parse: %w", err)
	}
	// 快手 result==1 为成功
	if r.Result != 1 {
		return nil, fmt.Errorf("kuaishou code2session: result=%d msg=%s", r.Result, r.ErrorMsg)
	}
	if r.OpenID == "" {
		return nil, errors.New("kuaishou code2session: empty open_id")
	}
	return &r, nil
}

func (s *Server) handleKuaishouMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.KuaishouMP == nil || !s.KuaishouMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "kuaishou_mp_not_configured", "")
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
	sess, err := s.KuaishouMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("kuaishou_mp.code2session", err)
		writeErr(w, http.StatusBadGateway, "kuaishou_exchange_failed", err.Error())
		return
	}
	rawProfile, _ := json.Marshal(map[string]any{"open_id": sess.OpenID})
	prof := providerProfile{
		Provider:       store.ProviderKuaishouMP,
		ProviderUserID: sess.OpenID,
		RawProfile:     rawProfile,
	}
	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("kuaishou_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if req.DeviceName == "" {
		req.DeviceName = "kuaishou-miniapp"
	}
	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
