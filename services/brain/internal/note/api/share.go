// 笔记分享管理端接口（requireAuth + 属主校验）。
//
//	PUT    /v1/notes/{id}/share         创建或更新（幂等，一篇一条；已停用=恢复原 token）
//	GET    /v1/notes/{id}/share         当前分享状态（含已停用）
//	DELETE /v1/notes/{id}/share         停用（disabled_at，可经 PUT 恢复）
//	POST   /v1/notes/{id}/share/rotate  重置 token + credential_version+1（旧链接即 404）
//	GET    /v1/notes/shares             我的分享列表（状态机 active/disabled/expired）
//
// 契约：docs/BiuMind-Technical-Architecture.md §7.6「API 契约（S1 冻结）」。
// 注意：分享接口的错误体是契约冻结的扁平形态 {"error":"<code>"}，
// 与本包其他端点的 {"error":{"code","message"}} 不同——跨端对齐基准
// 以契约为准，客户端按字符串解析。
package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// shareErrMessages —— 分享接口错误码 → 英文消息（对齐 note 域 writeErr 风格）。
var shareErrMessages = map[string]string{
	"not_found":         "share not found or disabled",
	"expired":           "share expired",
	"exhausted":         "share view limit exhausted",
	"note_deleted":      "note deleted",
	"password_required": "password required",
	"invalid_password":  "invalid password",
	"bad_request":       "bad request",
	"bad_expires_in":    "invalid expires_in, expect 1d/7d/30d/never",
	"bad_password":      "password must be 4-8 characters",
	"bad_max_views":     "max_views must be a positive integer, or 0 to remove the limit",
	"files_unavailable": "files storage unavailable",
	"internal":          "internal error",
}

// writeShareErr —— 分享接口错误体，与 note 域既有 writeErr 同风格：
// {"error":{"code":"<code>","message":"<msg>"}}（Flutter / Astro 仅按
// HTTP 状态码分支，不解析 body；§7.6 契约以此为准）。
func writeShareErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": shareErrMessages[code]},
	})
}

// generateShareToken —— crypto/rand 24 字节 → 32 字符 base64url
// （不可猜测即第一道防线，设计 D2）。
func generateShareToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// shareOut —— 管理端 share 对象（§7.6 冻结字段 + S2 max_views；不返回
// url，客户端用 origin 自拼 ${origin}/s/${token}）。
func shareOut(sh *store.Share) map[string]any {
	out := map[string]any{
		"token":              sh.Token,
		"password_set":       sh.PasswordHash != nil,
		"credential_version": sh.CredentialVersion,
		"view_count":         sh.ViewCount,
		"created_at":         sh.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":         sh.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if sh.ExpiresAt != nil {
		out["expires_at"] = sh.ExpiresAt.UTC().Format(time.RFC3339)
	} else {
		out["expires_at"] = nil
	}
	if sh.MaxViews != nil {
		out["max_views"] = *sh.MaxViews
	} else {
		out["max_views"] = nil
	}
	if sh.DisabledAt != nil {
		out["disabled_at"] = sh.DisabledAt.UTC().Format(time.RFC3339)
	} else {
		out["disabled_at"] = nil
	}
	return out
}

// shareStatus —— 管理列表状态机：disabled > exhausted > expired > active
// （S2 契约：exhausted = max_views 非空且 view_count 已达上限）。
func shareStatus(sh *store.Share, now time.Time) string {
	if sh.DisabledAt != nil {
		return "disabled"
	}
	if sh.MaxViews != nil && sh.ViewCount >= int64(*sh.MaxViews) {
		return "exhausted"
	}
	if sh.ExpiresAt != nil && now.After(*sh.ExpiresAt) {
		return "expired"
	}
	return "active"
}

// getOwnedNote —— 管理端属主校验：笔记存在、属于本人、未进回收站。
func (s *Server) getOwnedNote(w http.ResponseWriter, r *http.Request, noteID uuid.UUID) bool {
	if _, err := s.Store.GetNote(r.Context(), noteID, mustUserID(r)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeShareErr(w, http.StatusNotFound, "not_found")
			return false
		}
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return false
	}
	return true
}

type putShareReq struct {
	// Password —— 字段缺省 = 保持不变；"" = 移除密码；有值 = 重设
	// （bcrypt + credential_version+1）。
	Password *string `json:"password"`
	// ExpiresIn —— 字段缺省 = 保持现有 expires_at 不变（新建时视为
	// never）；有值时 "1d" | "7d" | "30d" | "never"，非法值 400。
	ExpiresIn *string `json:"expires_in"`
	// MaxViews —— S2 三态，与 password / expires_in 同套规则：字段缺省
	// （或 null）= 保持不变；0 = 移除上限；正整数 = 设置/调整。
	// RawMessage 承载是为了把「非数 / 小数 / 负数」精确映射到
	// 400 bad_max_views 而非笼统的 bad_request。
	MaxViews json.RawMessage `json:"max_views"`
}

// parseMaxViews —— max_views 三态解析。ok=false 时响应已写。
// 列是 PG int4，超 int32 范围一并按非法值拒。
func parseMaxViews(w http.ResponseWriter, raw json.RawMessage, in *store.UpsertShareInput) (ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return true // 缺省 = 保持
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_max_views")
		return false
	}
	v, err := num.Int64() // "2.5" 等小数值在此失败
	if err != nil || v < 0 || v > math.MaxInt32 {
		writeShareErr(w, http.StatusBadRequest, "bad_max_views")
		return false
	}
	in.MaxViewsSet = true
	if v > 0 {
		mv := int(v)
		in.MaxViews = &mv
	}
	// v == 0 → MaxViews 保持 nil = 移除上限
	return true
}

func (s *Server) handlePutShare(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	var req putShareReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	uid := mustUserID(r)
	if !s.getOwnedNote(w, r, id) {
		return
	}

	in := store.UpsertShareInput{
		NoteID: id, UserID: uid, ActorID: uid.String(),
	}
	if req.ExpiresIn != nil {
		in.ExpiresSet = true
		switch *req.ExpiresIn {
		case "1d":
			t := time.Now().Add(24 * time.Hour)
			in.ExpiresAt = &t
		case "7d":
			t := time.Now().Add(7 * 24 * time.Hour)
			in.ExpiresAt = &t
		case "30d":
			t := time.Now().Add(30 * 24 * time.Hour)
			in.ExpiresAt = &t
		case "never":
			// expires_at = NULL
		default:
			writeShareErr(w, http.StatusBadRequest, "bad_expires_in")
			return
		}
	}
	if req.Password != nil {
		in.PasswordSet = true
		if *req.Password != "" {
			// 设计 D2：密码 4–8 位
			if n := utf8.RuneCountInString(*req.Password); n < 4 || n > 8 {
				writeShareErr(w, http.StatusBadRequest, "bad_password")
				return
			}
			hash, herr := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
			if herr != nil {
				writeShareErr(w, http.StatusInternalServerError, "internal")
				return
			}
			h := string(hash)
			in.PasswordHash = &h
		}
	}
	if !parseMaxViews(w, req.MaxViews, &in) {
		return
	}
	if in.Token == "" {
		tok, terr := generateShareToken()
		if terr != nil {
			writeShareErr(w, http.StatusInternalServerError, "internal")
			return
		}
		in.Token = tok
	}
	sh, err := s.Store.UpsertShare(r.Context(), in)
	if err != nil {
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, shareOut(sh))
}

func (s *Server) handleGetShare(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	sh, err := s.Store.GetShareByNote(r.Context(), id, mustUserID(r))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeShareErr(w, http.StatusNotFound, "not_found")
			return
		}
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, shareOut(sh))
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	uid := mustUserID(r)
	if err := s.Store.DisableShare(r.Context(), id, uid, uid.String()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeShareErr(w, http.StatusNotFound, "not_found")
			return
		}
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRotateShare(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	uid := mustUserID(r)
	tok, err := generateShareToken()
	if err != nil {
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	sh, err := s.Store.RotateShare(r.Context(), id, uid, tok, uid.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeShareErr(w, http.StatusNotFound, "not_found")
			return
		}
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, shareOut(sh))
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	shares, err := s.Store.ListShares(r.Context(), mustUserID(r))
	if err != nil {
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	now := time.Now()
	out := make([]map[string]any, 0, len(shares))
	for _, sw := range shares {
		o := shareOut(&sw.Share)
		o["note_id"] = sw.NoteID.String()
		o["note_title"] = sw.NoteTitle
		o["status"] = shareStatus(&sw.Share, now)
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": out})
}
