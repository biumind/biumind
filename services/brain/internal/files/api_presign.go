// Two-step upload: presign-upload + finalize.
//
// Why two steps:
//   * presign-upload returns a short-lived signed PUT URL — client streams
//     bytes directly to MinIO, skipping the brain proxy
//   * finalize confirms MinIO actually has the bytes (via Blob.Head),
//     records sha256/size for dedup, flips the row from 'pending' → 'ready'
//
// Compared to the older /v1/files/upload (multipart proxy):
//   * brain doesn't touch the bytes — bandwidth + memory savings on big files
//   * client and MinIO share the work; brain only mediates metadata
//
// 设计文档: docs/BiuMind-Chat-Attachments-MinIO-Design.md §4.1–§4.2.

package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	presignUploadTTL = 15 * time.Minute
	// presignGetTTL — 给 LLM provider / 渲染场景的下载 URL TTL。
	// 15 分钟覆盖一次最长 LLM 单轮 (含重试) 而不显著扩大泄露窗口。
	presignGetTTL = 15 * time.Minute
)

// allowedMimePrefixes — 允许直传的 mime 类型白名单。聊天附件目前只
// 用图片 + PDF; 其他来源 (e.g. code-artifact) 走代理上传 /v1/files/upload。
var allowedMimePrefixes = []string{
	"image/",
	"application/pdf",
}

// handlePresignUpload — 两段式上传第 1 步: 申请预签名 PUT URL。
//
// 请求 body (JSON):
//
//	{
//	  "filename":  "screenshot.png",
//	  "mime":      "image/png",
//	  "size":      245760,
//	  "source":    "chat-attachment",
//	  "metadata":  {"thread_id": "..."}    // optional
//	}
//
// 响应:
//
//	{
//	  "file_id":    "01H...",
//	  "upload_url": "https://...?X-Amz-Signature=...",
//	  "headers":    {"Content-Type": "image/png"},
//	  "expires_at": "2026-05-29T10:30:00Z"
//	}
func (s *Server) handlePresignUpload(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)

	var in struct {
		Filename string          `json:"filename"`
		Mime     string          `json:"mime"`
		Size     int64           `json:"size"`
		Source   string          `json:"source"`
		Metadata json.RawMessage `json:"metadata,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if in.Mime == "" {
		writeErr(w, http.StatusBadRequest, "missing_mime", "mime required")
		return
	}
	if !mimeAllowed(in.Mime) {
		writeErr(w, http.StatusBadRequest, "mime_not_allowed",
			fmt.Sprintf("mime %q not in allow-list", in.Mime))
		return
	}
	if in.Size <= 0 {
		writeErr(w, http.StatusBadRequest, "bad_size", "size must be > 0")
		return
	}
	if s.MaxUploadBytes > 0 && in.Size > s.MaxUploadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("size %d exceeds limit %d", in.Size, s.MaxUploadBytes))
		return
	}
	if in.Source == "" {
		in.Source = "unknown"
	}
	if len(in.Metadata) > 0 && !json.Valid(in.Metadata) {
		writeErr(w, http.StatusBadRequest, "bad_metadata", "metadata must be valid JSON")
		return
	}

	id := uuid.New()
	objectKey := fmt.Sprintf("%s/%s", uid.String(), id.String())

	// 预占位 — finalize 会找这一行升级到 ready。
	if err := s.Store.Insert(r.Context(), Object{
		ID:        id,
		UserID:    uid,
		Sha256:    "", // pending 阶段允许空
		SizeBytes: in.Size,
		MimeType:  &in.Mime,
		Bucket:    s.Blob.Bucket(),
		ObjectKey: objectKey,
		Source:    in.Source,
		Status:    StatusPending,
		Metadata:  in.Metadata,
	}); err != nil {
		s.serverErr(w, "presign insert pending", err)
		return
	}

	signed, err := s.Blob.PresignPut(r.Context(), objectKey, presignUploadTTL, in.Mime)
	if err != nil {
		// 留 pending 行给 GC 清; 不阻塞 client 重试。
		s.serverErr(w, "presign sign", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"file_id":    id.String(),
		"upload_url": signed.String(),
		"headers":    map[string]string{"Content-Type": in.Mime},
		"expires_at": time.Now().Add(presignUploadTTL).UTC(),
		"object_key": objectKey,
	})
}

// handleFinalize — 两段式上传第 2 步: 确认对象已上来 + 写最终元数据。
//
// 请求 body:
//
//	{"file_id": "01H...", "sha256": "abc...", "size": 245760}
//
// 服务端:
//  1. 取 pending 行 (按 user 严格匹配)
//  2. Blob.Head 验对象真存在 + 真实 size 与声明匹配
//  3. dedup: 用户内已有同 sha256 的 ready 对象 → 删自己刚 PUT 的 +
//     删自己的 pending 行 → 返回旧 file_id (deduped:true)
//  4. 否则 MarkReady → 返回新 file_id (deduped:false)
func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	uid := mustUserID(r)
	var in struct {
		FileID string `json:"file_id"`
		Sha256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	id, err := uuid.Parse(in.FileID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_file_id", err.Error())
		return
	}
	if len(in.Sha256) != 64 || !isHex(in.Sha256) {
		writeErr(w, http.StatusBadRequest, "bad_sha256", "sha256 must be 64-char hex")
		return
	}
	if in.Size <= 0 {
		writeErr(w, http.StatusBadRequest, "bad_size", "size must be > 0")
		return
	}

	pending, err := s.Store.GetPending(r.Context(), uid, id)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}

	info, err := s.Blob.Head(r.Context(), pending.ObjectKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "blob_missing",
				"client did not upload bytes before finalize")
			return
		}
		s.serverErr(w, "head blob", err)
		return
	}
	if info.Size != in.Size {
		writeErr(w, http.StatusBadRequest, "size_mismatch",
			fmt.Sprintf("blob size %d != claimed %d", info.Size, in.Size))
		return
	}
	if s.MaxUploadBytes > 0 && info.Size > s.MaxUploadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("blob size %d exceeds limit %d", info.Size, s.MaxUploadBytes))
		return
	}

	// dedup 检查 — 用户内已有同 sha256 的 ready 对象?
	if existing, err := s.Store.LookupBySha256(r.Context(), uid, in.Sha256); err != nil {
		s.serverErr(w, "lookup sha256", err)
		return
	} else if existing != nil {
		// 撤掉自己刚 PUT 的 + 删 pending 行, 返回已有 file_id。
		_ = s.Blob.Remove(r.Context(), pending.ObjectKey)
		_ = s.Store.HardDelete(r.Context(), uid, id)
		writeJSON(w, http.StatusOK, finalizeOut(existing, true))
		return
	}

	if err := s.Store.MarkReady(r.Context(), uid, id, in.Sha256, info.Size); err != nil {
		s.handleStoreErr(w, err)
		return
	}
	// 取最新行回应 (status=ready, sha256/size 已写入)。
	obj, err := s.Store.Get(r.Context(), uid, id)
	if err != nil {
		s.handleStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, finalizeOut(obj, false))
}

// finalizeOut — finalize 的标准响应体。比 objectOut 多 deduped 标志,
// 客户端依此判断是否复用了已有对象 (UI 可以提示 "已存在, 节省了上传")。
func finalizeOut(o *Object, deduped bool) map[string]any {
	out := map[string]any{
		"file_id":    o.ID.String(),
		"sha256":     o.Sha256,
		"size_bytes": o.SizeBytes,
		"bucket":     o.Bucket,
		"deduped":    deduped,
		"created_at": o.CreatedAt,
	}
	if o.MimeType != nil {
		out["mime_type"] = *o.MimeType
	}
	return out
}

// handlePresignGet — 给 model-relay adapter (cloud LLM call) 用的内部端点:
// 拿短时效 GET URL 转给上游 LLM provider 当 image url。
//
// 鉴权: 复用 Bearer (model-relay 转发用户 JWT)。Store.Get 过滤了非 ready /
// 跨 user / soft-deleted 的对象, 这里再返回 404 即可。
//
// 响应:
//
//	{"url": "https://...?X-Amz-Signature=...", "media_type": "image/png", "expires_at": "..."}
//
// 安全: URL 不写日志 (model-relay 侧需 redact); audit log 仅记 user_id + file_id +
// 调用方 IP。
func (s *Server) handlePresignGet(w http.ResponseWriter, r *http.Request) {
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
	signed, err := s.Blob.PresignGet(r.Context(), obj.ObjectKey, presignGetTTL)
	if err != nil {
		s.serverErr(w, "presign get sign", err)
		return
	}
	if s.Logger != nil {
		// audit (URL 不进日志)
		s.Logger.Info("presign-get",
			"user_id", uid.String(),
			"file_id", id.String(),
			"remote", r.RemoteAddr,
		)
	}
	out := map[string]any{
		"url":        signed.String(),
		"expires_at": time.Now().Add(presignGetTTL).UTC(),
	}
	if obj.MimeType != nil {
		out["media_type"] = *obj.MimeType
	}
	writeJSON(w, http.StatusOK, out)
}

func mimeAllowed(m string) bool {
	for _, p := range allowedMimePrefixes {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
