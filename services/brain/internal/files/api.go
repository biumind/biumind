// REST handlers for file upload / download. Mounted at /v1/files/*.
//
// 路由:
//   POST /v1/files/upload                multipart, 字段 'file' + 可选 'metadata' JSON
//   GET  /v1/files/{id}                  流式下载, 透传 Content-Type/Length
//   GET  /v1/files/{id}/meta             仅元数据 (size / sha256 / mime)
//   DELETE /v1/files/{id}                soft delete (实际 blob 留给清理 job)
//
// 鉴权同 brain code 模块: Bearer JWT, mustUserID 严格匹配。

package files

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

type Server struct {
	Store    *Store
	Blob     *Blob
	Verifier *bauth.Verifier
	Logger   *slog.Logger

	// MaxUploadBytes — 单文件上限. 0 = 不限. 一般 100MB - 1GB。
	MaxUploadBytes int64
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/files/upload", s.requireAuth(s.handleUpload))
	mux.HandleFunc("POST /v1/files/presign-upload", s.requireAuth(s.handlePresignUpload))
	mux.HandleFunc("POST /v1/files/finalize", s.requireAuth(s.handleFinalize))
	mux.HandleFunc("POST /v1/files/{id}/presign-get", s.requireAuth(s.handlePresignGet))
	// ⚠️ 不能挂在 /v1/files/ 下面 —— Go 1.22+ ServeMux 会 panic: 任何
	// /v1/files/<字面量>/{x} (4 段) 与已有的 /v1/files/{id}/meta (4 段, 通配在
	// 第 3 段) 互不更具体, 冲突 (都能匹配 /v1/files/by-sha/meta)。故用独立
	// 命名空间 /v1/brain/, 与 aigc 的 /v1/aigc/files-by-sha 对称消歧 (site
	// nginx 单 origin 按路径反代)。
	mux.HandleFunc("GET /v1/brain/files-by-sha/{sha}", s.requireAuth(s.handleDownloadBySha))
	mux.HandleFunc("GET /v1/files/{id}", s.requireAuth(s.handleDownload))
	mux.HandleFunc("GET /v1/files/{id}/meta", s.requireAuth(s.handleMeta))
	mux.HandleFunc("DELETE /v1/files/{id}", s.requireAuth(s.handleDelete))
}

// ─── Upload ───────────────────────────────────────────────

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	if s.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadBytes+1024)
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_multipart", err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck

	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_file", "expect multipart field 'file'")
		return
	}
	defer file.Close()

	source := r.FormValue("source")
	if source == "" {
		source = "unknown"
	}
	metaRaw := r.FormValue("metadata")
	var metaJSON json.RawMessage
	if metaRaw != "" {
		if !json.Valid([]byte(metaRaw)) {
			writeErr(w, http.StatusBadRequest, "bad_metadata", "metadata field must be valid JSON")
			return
		}
		metaJSON = json.RawMessage(metaRaw)
	}

	// 一遍读, 同时算 sha256 + 落临时文件 (PutObject 需要 io.Reader 单 pass,
	// dedup 又要 sha256 知道才能查 — 所以中间落临时盘换两遍读)。
	tmp, err := os.CreateTemp("", "biumind-upload-*")
	if err != nil {
		s.serverErr(w, "tmp create", err)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), file)
	if err != nil {
		_ = tmp.Close()
		s.serverErr(w, "tmp write", err)
		return
	}
	if err := tmp.Close(); err != nil {
		s.serverErr(w, "tmp close", err)
		return
	}
	if s.MaxUploadBytes > 0 && written > s.MaxUploadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("upload exceeds limit %d bytes", s.MaxUploadBytes))
		return
	}
	shaHex := hex.EncodeToString(hasher.Sum(nil))

	// Dedup: 用户已有同 sha256 → 复用 object_key, 不重新 PutObject
	existing, err := s.Store.LookupBySha256(r.Context(), uid, shaHex)
	if err != nil {
		s.serverErr(w, "lookup", err)
		return
	}
	if existing != nil {
		writeJSON(w, http.StatusOK, objectOut(existing, true))
		return
	}

	// 没命中 → 上传 MinIO + 写元数据.
	id := uuid.New()
	objectKey := fmt.Sprintf("%s/%s", uid.String(), id.String())
	mime := hdr.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}

	// 重新打开临时文件流给 PutObject (读两遍一次 sha 一次 upload)
	f2, err := os.Open(tmpPath)
	if err != nil {
		s.serverErr(w, "tmp reopen", err)
		return
	}
	defer f2.Close()

	if err := s.Blob.Put(r.Context(), objectKey, f2, written, mime); err != nil {
		s.serverErr(w, "blob put", err)
		return
	}

	obj := Object{
		ID:        id,
		UserID:    uid,
		Sha256:    shaHex,
		SizeBytes: written,
		MimeType:  &mime,
		Bucket:    s.Blob.Bucket(),
		ObjectKey: objectKey,
		Source:    source,
		Metadata:  metaJSON,
	}
	if err := s.Store.Insert(r.Context(), obj); err != nil {
		// 幂等保险: 万一 race 中另一个请求先 Insert 了同 sha256, lookup
		// 再试一次返回 (dedup window race)
		if again, _ := s.Store.LookupBySha256(r.Context(), uid, shaHex); again != nil {
			// 自己的 PutObject 已经成功; 留着无害, 清理 job 后续按 orphan
			// 删 (P2 后续优化)
			writeJSON(w, http.StatusOK, objectOut(again, true))
			return
		}
		s.serverErr(w, "insert metadata", err)
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "files api: upload",
			"user_id", uid, "object_id", id, "bytes", written,
			"sha256", shaHex, "mime", mime, "source", source)
	}
	writeJSON(w, http.StatusOK, objectOut(&obj, false))
}

// ─── Download ─────────────────────────────────────────────

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	obj, err := s.Store.Get(r.Context(), uid, id)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	rc, info, err := s.Blob.Get(r.Context(), obj.ObjectKey)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	defer rc.Close()
	if obj.MimeType != nil {
		w.Header().Set("Content-Type", *obj.MimeType)
	}
	if info != nil && info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, obj.ID.String()))
	if _, err := io.Copy(w, rc); err != nil {
		s.Logger.Warn("blob download stream interrupted", "err", err, "id", id)
	}
}

// handleDownloadBySha — `GET /v1/brain/files-by-sha/{sha}`. 复用 LookupBySha256
// 找到当前 user 在 sha 下 ready 的对象后走 download 流式逻辑。提供给
// 不持久化 file_id (仅有 hash) 的调用方, 比如 sidebar 渲染 user_webview
// app 图标 — manifest.icon 字段写 "cas:<sha>" 让客户端直接 GET 这条。
//
// sha256 是 user-scoped (LookupBySha256 第一个参数是 user_id), 跨 user
// 不可见 → 不会泄漏, 同时不需要额外鉴权检查。
func (s *Server) handleDownloadBySha(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	sha := strings.ToLower(strings.TrimSpace(r.PathValue("sha")))
	if len(sha) != 64 {
		writeErr(w, http.StatusBadRequest, "bad_sha", "sha256 must be 64 hex chars")
		return
	}
	for _, c := range sha {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			writeErr(w, http.StatusBadRequest, "bad_sha", "sha256 contains non-hex chars")
			return
		}
	}
	obj, err := s.Store.LookupBySha256(r.Context(), uid, sha)
	if err != nil {
		s.serverErr(w, "lookup", err)
		return
	}
	if obj == nil {
		writeErr(w, http.StatusNotFound, "not_found", "no ready object with this sha for caller")
		return
	}
	rc, info, err := s.Blob.Get(r.Context(), obj.ObjectKey)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	defer rc.Close()
	if obj.MimeType != nil {
		w.Header().Set("Content-Type", *obj.MimeType)
	}
	if info != nil && info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	// 长期可缓存 — sha 内容是 immutable。
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if _, err := io.Copy(w, rc); err != nil {
		s.Logger.Warn("blob by-sha download stream interrupted", "err", err, "sha", sha)
	}
}

// ─── Meta + Delete ────────────────────────────────────────

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	obj, err := s.Store.Get(r.Context(), uid, id)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, objectOut(obj, false))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	if err := s.Store.SoftDelete(r.Context(), uid, id); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── helpers ──────────────────────────────────────────────

func objectOut(o *Object, deduped bool) map[string]any {
	out := map[string]any{
		"id":         o.ID.String(),
		"sha256":     o.Sha256,
		"size_bytes": o.SizeBytes,
		"bucket":     o.Bucket,
		"created_at": o.CreatedAt,
		"deduped":    deduped,
	}
	if o.MimeType != nil {
		out["mime_type"] = *o.MimeType
	}
	if len(o.Metadata) > 0 {
		out["metadata"] = json.RawMessage(o.Metadata)
	}
	return out
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func mustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
}

func (s *Server) handleStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrInvalid):
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		s.serverErr(w, "files", err)
	}
}

func (s *Server) serverErr(w http.ResponseWriter, where string, err error) {
	s.Logger.Error("files server error", "where", where, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", err.Error())
}

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
