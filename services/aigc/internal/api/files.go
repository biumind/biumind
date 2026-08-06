// files.go — CAS 产物下载 endpoint.
//
//	GET /v1/aigc/files-by-sha/{sha}
//
// 客户端 OutputThumbnail (output_thumbnail.dart) 把 task_outputs 的
// storage_url=cas:<sha> 解析成 {origin}/v1/aigc/files-by-sha/<sha> + Bearer,
// 命中后流式回源 MinIO outputs / derivatives 桶。
//
// 鉴权: requireAuth 已验 JWT; 这里再校验 sha 归属 —— 必须是当前用户自己的
// 产物, 或属于公开 (is_public) 的产物, 否则 403 (防越权拉别人私有图)。
package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/biumind/biumind/services/aigc/internal/blob"
	"github.com/biumind/biumind/services/aigc/internal/store"
)

// BlobGetter — handler 对对象存储的最小依赖. 生产是 *blob.Client, 单测注入
// fake (不起真 MinIO)。
type BlobGetter interface {
	Get(ctx context.Context, logicalBucket, objectKey string) (io.ReadCloser, *blob.ObjectInfo, error)
}

func (s *Server) handleDownloadBySha(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if s.Blob == nil {
		writeErr(w, http.StatusServiceUnavailable, "blob_not_wired",
			"object storage not configured")
		return
	}
	sha := r.PathValue("sha")
	if len(sha) != 64 {
		writeErr(w, http.StatusBadRequest, "bad_sha", "sha must be 64 hex chars")
		return
	}

	loc, err := s.Store.LookupOutputBySha(r.Context(), sha)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "no output with that sha")
			return
		}
		writeErr(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}

	// 归属校验: 自己的产物 or 公开产物才放行.
	if loc.OwnerUserID != uid && !loc.IsPublic {
		writeErr(w, http.StatusForbidden, "forbidden", "not your asset")
		return
	}

	rc, info, err := s.Blob.Get(r.Context(), loc.Bucket, loc.StorageKey)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			// DB 有记录但对象不在桶里 (转存中途失败 / 已清理) — 404 而非 500.
			writeErr(w, http.StatusNotFound, "object_missing",
				"asset metadata exists but blob not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "blob_get_failed", err.Error())
		return
	}
	defer rc.Close()

	ct := loc.MimeType
	if ct == "" && info != nil && info.ContentType != "" {
		ct = info.ContentType
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if info != nil && info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	// CAS 内容不可变 (sha 即指纹), 可激进缓存.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}
