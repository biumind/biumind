package api

// alipay_mp.go — 支付宝小程序登录端点.
//
//	POST /v1/auth/alipay/mp-login   { code, installation_id, device_name }
//
// ⚠️ 实现状态: handler 骨架就绪; code2session 待接 RSA2 签名.
//
// 支付宝走 OpenAPI Gateway (https://openapi.alipay.com/gateway.do), 调用
// alipay.system.oauth.token 方法用 authCode 换 access_token + user_id.
// 请求需要 RSA2 签名 — 后续接入时建议引入 github.com/smartwalle/alipay/v3
// 或自实现签名: 这里留 TODO 标记, 不阻塞其他平台编译.
//
// PrivateKey 走 BYOK 信封加密, 启动从 KMS 加载.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/identity/internal/store"
)

type AlipayMPConfig struct {
	AppID      string
	PrivateKey string // RSA2 私钥 PEM, 走 BYOK 信封加密注入
	PublicKey  string // 支付宝公钥 (验签用)
	HTTPClient *http.Client
}

func (c *AlipayMPConfig) Configured() bool {
	return c != nil && c.AppID != "" && c.PrivateKey != ""
}

type alipayOAuthResp struct {
	UserID      string
	AccessToken string
}

// code2session — 真实实现需要 RSA2 签名 + 调 alipay.system.oauth.token.
// 当前留 TODO 桩, 接入时替换. 不影响其他平台路径.
func (c *AlipayMPConfig) code2session(_ context.Context, _ string) (*alipayOAuthResp, error) {
	// TODO: RSA2 sign + POST alipay.system.oauth.token, parse user_id
	return nil, errors.New("alipay_mp: code2session not yet implemented (RSA2 signing)")
}

func (s *Server) handleAlipayMPLogin(w http.ResponseWriter, r *http.Request) {
	if s.AlipayMP == nil || !s.AlipayMP.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "alipay_mp_not_configured", "")
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
	sess, err := s.AlipayMP.code2session(r.Context(), req.Code)
	if err != nil {
		s.logErr("alipay_mp.code2session", err)
		writeErr(w, http.StatusNotImplemented, "alipay_exchange_failed", err.Error())
		return
	}
	rawProfile, _ := json.Marshal(map[string]any{"user_id": sess.UserID})
	prof := providerProfile{
		Provider:       store.ProviderAlipayMP,
		ProviderUserID: sess.UserID,
		RawProfile:     rawProfile,
	}
	u, _, err := s.resolveOrCreateUserByProvider(r.Context(), prof)
	if err != nil {
		s.logErr("alipay_mp.resolveOrCreate", err)
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if req.DeviceName == "" {
		req.DeviceName = "alipay-miniapp"
	}
	s.mpRespondWithTokens(w, r.Context(), u, req.DeviceName, req.InstallationID)
}
