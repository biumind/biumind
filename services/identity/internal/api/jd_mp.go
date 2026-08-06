package api

// jd_mp.go — 京东小程序登录端点.
//
//	POST /v1/auth/jd/mp-login   { code, installation_id, device_name }
//
// ⚠️ 实现状态: handler 骨架就绪; code2session 待接京东开放平台 SDK.
//
// 京东小程序走 jdcloud-openplatform-mp gateway, 需要 sign + appkey/secret.
// 当前留 TODO, 接入时替换 code2session 内部即可.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/identity/internal/store"
)

type JDMPConfig struct {
	AppID      string
	AppSecret  string
	HTTPClient *http.Client
}

func (c *JDMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.AppSecret != ""
}

type jdCode2SessionResp struct {
	OpenID     string
	SessionKey string
}

func (c *JDMPConfig) code2session(_ context.Context, _ string) (*jdCode2SessionResp, error) {
	// TODO: 京东开放平台 sign 算法 + jscode2session 调用
	return nil, errors.New("jd_mp: code2session not yet implemented")
}

func (s *Server) handleJDMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.JDMP == nil || !s.JDMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "jd_mp_not_configured", "")
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
	sess, err := s.JDMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("jd_mp.code2session", err)
		writeErr(w, http.StatusNotImplemented, "jd_exchange_failed", err.Error())
		return
	}
	rawProfile, _ := json.Marshal(map[string]any{"openid": sess.OpenID})
	prof := providerProfile{
		Provider:       store.ProviderJDMP,
		ProviderUserID: sess.OpenID,
		RawProfile:     rawProfile,
	}
	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("jd_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if req.DeviceName == "" {
		req.DeviceName = "jd-miniapp"
	}
	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
