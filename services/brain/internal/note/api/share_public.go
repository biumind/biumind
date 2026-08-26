// 笔记分享公开端接口（无鉴权 —— brain 首批公开业务路由，与既有
// requireAuth 端点显式分离，挂 MountPublic 而非 Mount）。
//
//	GET  /v1/shares/{token}                笔记内容（biu-file:// 已改写；有密码未解锁 → 401）
//	POST /v1/shares/{token}/unlock         校验密码 → 签短期访问 JWT（HS256，exp 2h）
//	GET  /v1/shares/{token}/files/{file_id} 附件代理（302 → 15min presign URL）
//
// 校验链（§7.6，每请求全量复核，撤销即时生效不等 exp）：
//
//	① share 行存在 且 disabled_at IS NULL 且未过 expires_at → 否则 404/410
//	② password_hash 非空 → 校验访问 JWT：签名 + exp +
//	   jwt.credential_version == share.credential_version    → 否则 401
//	③（files 路由）file_id ∈ note_attachments(note_id)      → 否则 404
//	④ note 未进回收站（deleted_at IS NULL）                 → 否则 410 note_deleted
//	⑤ files 路由：302 → 15min presign URL（MinIO 直出，
//	   响应附 nosniff + CSP sandbox）
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/services/brain/internal/note/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// shareFilePresignTTL —— 附件 302 目标的 presign GET TTL（同 files 域
// presignGetTTL 的 15 分钟惯例）。
const shareFilePresignTTL = 15 * time.Minute

// MountPublic —— 公开分享路由（无 requireAuth）。限流在 nginx 层
// （/v1/shares 通用 zone + unlock 更严 location），brain 内不做。
func (s *Server) MountPublic(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/shares/{token}", s.handlePublicGetShare)
	mux.HandleFunc("POST /v1/shares/{token}/unlock", s.handlePublicUnlockShare)
	mux.HandleFunc("GET /v1/shares/{token}/files/{file_id}", s.handlePublicShareFile)
}

// resolveActiveShare —— 校验链①：行存在（404 not_found）+ 未停用
// （404 not_found——不暴露「存在但停用」）+ 未过期（410 expired）+
// 未达访问上限（410 exhausted，S2 契约：追加在 expired 之后，同码不同
// code）。返回 nil 时响应已写。
func (s *Server) resolveActiveShare(w http.ResponseWriter, r *http.Request) *store.Share {
	sh, err := s.Store.GetShareByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeShareErr(w, http.StatusNotFound, "not_found")
			return nil
		}
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return nil
	}
	if sh.DisabledAt != nil {
		writeShareErr(w, http.StatusNotFound, "not_found")
		return nil
	}
	if sh.ExpiresAt != nil && time.Now().After(*sh.ExpiresAt) {
		writeShareErr(w, http.StatusGone, "expired")
		return nil
	}
	if sh.MaxViews != nil && sh.ViewCount >= int64(*sh.MaxViews) {
		writeShareErr(w, http.StatusGone, "exhausted")
		return nil
	}
	return sh
}

// checkShareAccess —— 校验链②：有密码的分享必须持有效访问 JWT
// （Bearer 或 ?access_token= 双通道）。
func (s *Server) checkShareAccess(w http.ResponseWriter, r *http.Request, sh *store.Share) bool {
	if sh.PasswordHash == nil {
		return true
	}
	if s.verifyShareAccess(r, sh) {
		return true
	}
	writeShareErr(w, http.StatusUnauthorized, "password_required")
	return false
}

// resolvePublicNote —— 校验链④：note 未进回收站，否则 410 note_deleted。
// 返回 nil 时响应已写。
func (s *Server) resolvePublicNote(w http.ResponseWriter, r *http.Request, noteID uuid.UUID) *store.PublicNote {
	n, err := s.Store.GetPublicNote(r.Context(), noteID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 硬删会 ON DELETE CASCADE 连带删 share 行，正常到不了这里；
			// 兜底按不存在处理。
			writeShareErr(w, http.StatusNotFound, "not_found")
			return nil
		}
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return nil
	}
	if n.DeletedAt != nil {
		writeShareErr(w, http.StatusGone, "note_deleted")
		return nil
	}
	return n
}

// handlePublicGetShare —— GET /v1/shares/{token}：标题 + 改写后
// content_md + 元信息；成功响应计一次访问（S2：带 X-Share-Session 的
// 会话内去重，未带 header 每次照计；files 路由不计数）。
func (s *Server) handlePublicGetShare(w http.ResponseWriter, r *http.Request) {
	sh := s.resolveActiveShare(w, r)
	if sh == nil || !s.checkShareAccess(w, r, sh) {
		return
	}
	n := s.resolvePublicNote(w, r, sh.NoteID)
	if n == nil {
		return
	}
	// S2 会话级去重：落地页每个浏览器会话上送 X-Share-Session
	// （sessionStorage 持有），服务端只落 sha256，首插成功才 +1。
	var sessionHash *string
	if sess := strings.TrimSpace(r.Header.Get("X-Share-Session")); sess != "" {
		sum := sha256.Sum256([]byte(sess))
		h := hex.EncodeToString(sum[:])
		sessionHash = &h
	}
	if _, err := s.Store.RecordShareView(r.Context(), sh.ID, sessionHash); err != nil && s.Logger != nil {
		s.Logger.Warn("note share: record view failed", "share_id", sh.ID, "err", err)
	}
	out := map[string]any{
		"title":             n.Title,
		"content_md":        store.RewriteShareFileURIs(n.ContentMD, sh.Token),
		"updated_at":        n.UpdatedAt.UTC().Format(time.RFC3339),
		"password_required": false,
	}
	if n.Author != nil {
		out["author"] = *n.Author
	} else {
		out["author"] = nil
	}
	if n.SourceURL != nil {
		out["source_url"] = *n.SourceURL
	} else {
		out["source_url"] = nil
	}
	writeJSON(w, http.StatusOK, out)
}

type unlockShareReq struct {
	Password string `json:"password"`
}

// handlePublicUnlockShare —— POST /v1/shares/{token}/unlock：bcrypt 校验
// → 签访问 JWT（share_id + credential_version，exp 2h）。密码失败写
// 审计事件（scope note_share）；按 IP 限流在 nginx 层（429 不由 brain
// 产生）。无密码的分享调用 unlock 直接签发（内容本就公开，客户端流程
// 统一，不引入额外错误码——契约未覆盖此分支）。
func (s *Server) handlePublicUnlockShare(w http.ResponseWriter, r *http.Request) {
	sh := s.resolveActiveShare(w, r)
	if sh == nil {
		return
	}
	var req unlockShareReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if sh.PasswordHash != nil {
		if bcrypt.CompareHashAndPassword([]byte(*sh.PasswordHash), []byte(req.Password)) != nil {
			// 审计：只记 id，不记密码明文 / IP（note_share 是全局审计 scope）。
			if err := s.Store.EmitShareEvent(r.Context(), "anonymous", "", store.ShareEventUnlockFailed, map[string]any{
				"share_id": sh.ID, "note_id": sh.NoteID, "user_id": sh.UserID,
			}); err != nil && s.Logger != nil {
				s.Logger.Warn("note share: unlock_failed audit write failed", "share_id", sh.ID, "err", err)
			}
			writeShareErr(w, http.StatusUnauthorized, "invalid_password")
			return
		}
	}
	tok, err := signShareAccess(s.ShareSigningKey, sh.ID, sh.CredentialVersion)
	if err != nil {
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok,
		"expires_in":   int(shareAccessTTL / time.Second),
	})
}

// handlePublicShareFile —— GET /v1/shares/{token}/files/{file_id}：
// 完整校验链后 302 到 15min presign URL（复用 files 域 Blob.PresignGet
// 内部逻辑，不走 HTTP 自调）。302 目标已是 MinIO URL，内容清洗由对象
// 存储侧响应头承担；本响应附 nosniff + CSP sandbox。
func (s *Server) handlePublicShareFile(w http.ResponseWriter, r *http.Request) {
	fileID, err := uuid.Parse(r.PathValue("file_id"))
	if err != nil {
		writeShareErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	sh := s.resolveActiveShare(w, r)
	if sh == nil || !s.checkShareAccess(w, r, sh) {
		return
	}
	// ③ 附件必须挂在该笔记上（防随机 ID 盗链）
	belongs, err := s.Store.AttachmentBelongs(r.Context(), sh.NoteID, fileID)
	if err != nil {
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	if !belongs {
		writeShareErr(w, http.StatusNotFound, "not_found")
		return
	}
	// ④ note 回收站复核（每请求全量，撤销即时生效）
	if s.resolvePublicNote(w, r, sh.NoteID) == nil {
		return
	}
	// ⑤ 302 → presign
	objectKey, err := s.Store.GetSharedFileObjectKey(r.Context(), fileID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeShareErr(w, http.StatusNotFound, "not_found")
			return
		}
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	if s.ShareBlob == nil {
		// MINIO_ENDPOINT 未配置的部署：契约无此分支，显式 503 而非 500。
		writeShareErr(w, http.StatusServiceUnavailable, "files_unavailable")
		return
	}
	signed, err := s.ShareBlob.PresignGet(r.Context(), objectKey, shareFilePresignTTL)
	if err != nil {
		writeShareErr(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.Redirect(w, r, signed.String(), http.StatusFound)
}
