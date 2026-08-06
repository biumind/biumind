package api

// me_profile.go — me 页面"完善资料"端点.
//
//	PATCH /v1/identity/me/profile  (Bearer)
//	  Body: { "display_name"?: string }
//	  → 200 { ok: true, user: {...} }
//
// 当前只允许改 display_name. avatar_url 暂存客户端 (微信 chooseAvatar
// 返回的是设备临时 URL, 跨设备同步要先上 OSS — W4-W5 接).
//
// 业务规则:
//   - display_name 长度 1..32, 去掉首尾空白
//   - 全空白视为"清空" → 拒绝 (不允许空 display_name; 让 me 页强制保留
//     用户主动设的名字, 否则会回退到 "微信用户" 等默认占位)

import (
	"encoding/json"
	"net/http"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

type updateProfileReq struct {
	DisplayName *string `json:"display_name,omitempty"`
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
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

	var req updateProfileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" {
			writeErr(w, http.StatusBadRequest, "empty_name", "display_name 不能为空")
			return
		}
		// 微信 nickname 最长 20 字, 给 32 留余量
		if utf8Len(name) > 32 {
			writeErr(w, http.StatusBadRequest, "name_too_long",
				"display_name 不能超过 32 字")
			return
		}
		if err := s.Store.UpdateUserDisplayName(r.Context(), uid, name); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}

	// 拉最新返回
	u, err := s.Store.GetUserByID(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"user": buildUserOut(u, s.RoleCache),
	})
}

// utf8Len — 按 Unicode codepoint 计数 (1 个汉字 = 1, 不是 3 字节).
func utf8Len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
