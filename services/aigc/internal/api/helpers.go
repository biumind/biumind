package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
)

// ─── JSON 响应 ────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

// ─── claim 抽取 ───────────────────────────────────────

// requireUserID 从 ctx 拿 user_id (requireAuth 已注入 claims). 失败时直接写 401.
func requireUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, *bauth.Claims, bool) {
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no_claims", "")
		return uuid.Nil, nil, false
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_subject", "")
		return uuid.Nil, nil, false
	}
	return uid, claims, true
}

// ─── 分页 ─────────────────────────────────────────────

func paginationFromQuery(q map[string][]string) (limit, offset int) {
	limit = 50
	if v := firstQ(q, "limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := firstQ(q, "offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

func firstQ(q map[string][]string, key string) string {
	if v, ok := q[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// firstRole 取 roles 切片首元素 (没有时返空), 给 authz attribute 用.
// Cedar policy 关心"有没有 role X", 暂时取主 role 简化; 多 role 决策走 attributes.roles 数组
// (后续 PrincipalUser 可扩展).
func firstRole(roles []string) string {
	if len(roles) == 0 {
		return ""
	}
	return roles[0]
}

// ─── store error 翻译 ─────────────────────────────────

// writeStoreErr 把 store sentinel 翻译成 HTTP 状态. 返回 true 表示已写响应.
func writeStoreErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
		return true
	}
	writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	return true
}
