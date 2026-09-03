// Package wikicommon —— 给新建的 wiki sub-packages（B0.5 一批 stub）共享
// 鉴权 + JSON 响应辅助。现有 wiki/{api,research,reviews,relevance,...}
// 各自带相同 boilerplate；本包仅给新模块（sources/activity/search/graph
// /chat/dedup/lint/llmsettings/suggestions）用，避免重复。
package wikicommon

import (
	"encoding/json"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

// RequireAuth 验证 Bearer token 并把 claims 注入 ctx。
func RequireAuth(v *bauth.Verifier, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			WriteErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := v.Verify(auth[7:])
		if err != nil {
			WriteErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

// MustUserID 从已注入的 claims 中取 user_id。仅在 RequireAuth 之后调用。
func MustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
}

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func WriteErr(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

// NotImplemented 给 stub 端点用。返回 501，body 含 module/endpoint 标识，
// 让客户端日志看出"这个端点是骨架，业务还没实现"。
func NotImplemented(w http.ResponseWriter, module, endpoint string) {
	WriteJSON(w, http.StatusNotImplemented, map[string]any{
		"error": map[string]any{
			"code":     "not_implemented",
			"message":  "endpoint stub; awaiting batch migration",
			"module":   module,
			"endpoint": endpoint,
		},
	})
}
