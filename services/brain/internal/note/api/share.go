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
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// writeShareErr —— 分享接口契约错误体：{"error":"<code>"}（扁平字符串，
// §7.6 冻结形态，Flutter / Astro 按此解析）。
func writeShareErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": code})
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

// shareOut —— 管理端 share 对象（§7.6 冻结字段；不返回 url，客户端用
// origin 自拼 ${origin}/s/${token}）。
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
	if sh.DisabledAt != nil {
		out["disabled_at"] = sh.DisabledAt.UTC().Format(time.RFC3339)
	} else {
		out["disabled_at"] = nil
	}
	return out
}

// shareStatus —— 管理列表状态机：disabled > expired > active。
func shareStatus(sh *store.Share, now time.Time) string {
	if sh.DisabledAt != nil {
		return "disabled"
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
	// ExpiresIn —— 每次必传："1d" | "7d" | "30d" | "never"。
	ExpiresIn string `json:"expires_in"`
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
	var expiresAt *time.Time
	switch req.ExpiresIn {
	case "1d":
		t := time.Now().Add(24 * time.Hour)
		expiresAt = &t
	case "7d":
		t := time.Now().Add(7 * 24 * time.Hour)
		expiresAt = &t
	case "30d":
		t := time.Now().Add(30 * 24 * time.Hour)
		expiresAt = &t
	case "never":
		// expires_at = NULL
	default:
		writeShareErr(w, http.StatusBadRequest, "bad_expires_in")
		return
	}
	uid := mustUserID(r)
	if !s.getOwnedNote(w, r, id) {
		return
	}

	in := store.UpsertShareInput{
		NoteID: id, UserID: uid, ExpiresAt: expiresAt, ActorID: uid.String(),
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
