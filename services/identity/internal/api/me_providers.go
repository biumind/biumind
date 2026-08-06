package api

// me_providers.go — me 页面的"我的账号"区域 API.
//
//	GET    /v1/identity/me/providers           列出当前 user 已绑定的第三方账号
//	DELETE /v1/identity/me/providers/{id}      解绑指定 provider 行
//
// 绑定 (POST) 不在这里 — 走具体平台的 mp-login / oauth-bind 流, 因为需要
// 拿外部 code 才能 verify. 这里只暴露列出 / 解绑.
//
// 解绑业务规则: 至少保留一种登录方式 (密码 OR ≥1 个第三方). 否则返 409.

import (
	"encoding/json"
	"errors"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/google/uuid"
)

type providerOut struct {
	ID             string  `json:"id"`
	Provider       string  `json:"provider"` // 'wechat_mp' / 'alipay_mp' / ...
	ProviderUserID string  `json:"provider_user_id"`
	Nickname       string  `json:"nickname,omitempty"`
	AvatarURL      string  `json:"avatar_url,omitempty"`
	BoundAt        string  `json:"bound_at"`
	LastLoginAt    *string `json:"last_login_at,omitempty"`
}

func buildProviderOut(p *store.IdentityProvider) providerOut {
	out := providerOut{
		ID:             p.ID.String(),
		Provider:       p.Provider,
		ProviderUserID: p.ProviderUserID,
		BoundAt:        p.BoundAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if p.LastLoginAt != nil {
		s := p.LastLoginAt.UTC().Format("2006-01-02T15:04:05Z")
		out.LastLoginAt = &s
	}
	if len(p.RawProfileJSON) > 0 {
		var m map[string]any
		if err := json.Unmarshal(p.RawProfileJSON, &m); err == nil {
			if v, ok := m["nickname"].(string); ok {
				out.Nickname = v
			}
			if v, ok := m["avatar_url"].(string); ok {
				out.AvatarURL = v
			}
		}
	}
	return out
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return
	}
	rows, err := s.Store.ListIdentityProvidersByUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]providerOut, 0, len(rows))
	for _, p := range rows {
		out = append(out, buildProviderOut(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *Server) handleUnbindProvider(w http.ResponseWriter, r *http.Request) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return
	}
	rawID := r.PathValue("id")
	pid, err := uuid.Parse(rawID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}

	// 业务规则: 解绑后至少保留 1 种登录方式 (密码 OR ≥1 个第三方).
	u, err := s.Store.GetUserByID(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	hasPassword := u.PasswordHash != nil && *u.PasswordHash != ""
	thirdPartyCount, err := s.Store.CountIdentityProvidersByUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// 解绑后剩余: hasPassword + (thirdPartyCount - 1)
	if !hasPassword && thirdPartyCount <= 1 {
		writeErr(w, http.StatusConflict, "last_login_method",
			"必须保留至少一种登录方式 — 请先设置密码或绑定其他第三方账号")
		return
	}

	if err := s.Store.DeleteIdentityProvider(r.Context(), pid, uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
