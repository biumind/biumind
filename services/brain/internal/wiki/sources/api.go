// Package sources HTTP API —— wiki 项目源文件管理。
//
// 路由：
//
//	POST   /v1/wiki/projects/{pid}/sources                       create / upsert source
//	GET    /v1/wiki/projects/{pid}/sources                       list project sources
//	DELETE /v1/wiki/projects/{pid}/sources/{sid}                 delete source
//	GET    /v1/wiki/projects/{pid}/sources/{sid}/delete-preview  pages affected if deleted
//	GET    /v1/wiki/projects/{pid}/sources/external-ids          dedupe by external id
//	(POST  /v1/wiki/projects/{pid}/sources/clip 已在 wiki/api 处理 webclip)
//
// 上传走两步：
//  1. 客户端先 POST /v1/files/upload （或 presign-upload）拿 file_id
//  2. 再 POST /v1/wiki/projects/{pid}/sources 用 file_id + rel_path 建关联
//
// 这样 wiki 维度只管"项目里有哪些源 + 解析状态"，二进制存储交给通用 files
// 模块（MinIO 后端 + cleanup job）。
//
// client-docproc（00007）：客户端本机解析后可跳过 file 上传，直接随请求
// 提交 extracted_text + content_hash(sha256 hex) + parse_meta，服务端做
// 尺寸上限/白名单校验 + 项目内 dedup，不再触发 wiki-parse worker。
package sources

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/biumind/biumind/services/brain/internal/wiki/wikicommon"
	"github.com/google/uuid"
)

const moduleName = "wiki.sources"

// maxExtractedTextBytes 是客户端随 source 直接提交解析文本（client-docproc
// 本机解析路径）的尺寸上限。超出按 413 拒绝 —— 更大文件应走 file_id 让
// 服务端 worker 解析。
const maxExtractedTextBytes = 8 << 20 // 8 MiB

// parseMetaKeys 是 parse_meta 白名单字段（00007 client-docproc），其余键丢弃。
// ocr_engine：B1 OCR provenance（客户端/服务端标记 OCR 引擎）。
var parseMetaKeys = map[string]bool{
	"parser": true, "version": true, "format": true, "page_count": true,
	"ocr_engine": true,
}

type Server struct {
	Store     *Store
	Wiki      *wikistore.Store
	Publisher publisher.Publisher // Phase 3：upload 入库后 publish wiki.parse 触发 parser；nil 时跳过（dev 无 NATS）
	Reviews   *reviews.Store      // client-docproc：随 source 提交解析文本时查项目内 dedup；nil → 跳过
	Verifier  *bauth.Verifier
	Logger    *slog.Logger
}

func NewServer(store *Store, wiki *wikistore.Store, p publisher.Publisher, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: store, Wiki: wiki, Publisher: p, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return wikicommon.RequireAuth(s.Verifier, h)
	}
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/sources", auth(s.handleCreate))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/sources", auth(s.handleList))
	mux.HandleFunc("DELETE /v1/wiki/projects/{pid}/sources/{sid}", auth(s.handleDelete))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/sources/{sid}/delete-preview", auth(s.handleDeletePreview))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/sources/external-ids", auth(s.handleExternalIDs))
}

// ─── Wire payloads ─────────────────────────────────────────────

type createReq struct {
	FileID         string         `json:"file_id,omitempty"`
	RelPath        string         `json:"rel_path"`
	Filename       string         `json:"filename,omitempty"`
	Mime           string         `json:"mime,omitempty"`
	ByteSize       int64          `json:"byte_size,omitempty"`
	ContentHashHex string         `json:"content_hash,omitempty"` // sha256 hex（与 internal parse-result 一致）
	ExtractedText  string         `json:"extracted_text,omitempty"`
	RawText        string         `json:"raw_text,omitempty"`     // extracted_text 的别名（docproc-web 客户端契约）
	ParseStatus    string         `json:"parse_status,omitempty"` // 默认 queued
	ExternalID     string         `json:"external_id,omitempty"`
	ParseMeta      map[string]any `json:"parse_meta,omitempty"` // client-docproc：parser/version/format/page_count
}

type sourceOut struct {
	ID          string         `json:"id"`
	ProjectID   string         `json:"project_id"`
	FileID      string         `json:"file_id,omitempty"`
	RelPath     string         `json:"rel_path"`
	Filename    string         `json:"filename"`
	Mime        string         `json:"mime,omitempty"`
	ByteSize    int64          `json:"byte_size"`
	ParseStatus string         `json:"parse_status"`
	ParseError  string         `json:"parse_error,omitempty"`
	ExternalID  string         `json:"external_id,omitempty"`
	ParseMeta   map[string]any `json:"parse_meta,omitempty"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

func sourceJSON(src *Source) sourceOut {
	out := sourceOut{
		ID:          src.ID.String(),
		ProjectID:   src.ProjectID.String(),
		RelPath:     src.RelPath,
		Filename:    src.Filename,
		Mime:        src.Mime,
		ByteSize:    src.ByteSize,
		ParseStatus: src.ParseStatus,
		ParseError:  src.ParseError,
		ExternalID:  src.ExternalID,
		CreatedAt:   src.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   src.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if src.FileID != nil {
		out.FileID = src.FileID.String()
	}
	if len(src.ParseMeta) > 0 {
		out.ParseMeta = src.ParseMeta
	}
	return out
}

// ─── Handlers ──────────────────────────────────────────────────

// normalizeCreateReq 校验并归一化 createReq：rel_path 必填、raw_text→
// extracted_text 别名合并（两字段同给时以 extracted_text 为准）、尺寸上限、
// content_hash hex 解码、parse_meta 白名单过滤。errCode 空串 = 通过；
// "extracted_text_too_large" 对应 413，其余对应 400。
func normalizeCreateReq(req *createReq) (contentHash []byte, parseMeta map[string]any, errCode string) {
	if req.RelPath == "" {
		return nil, nil, "missing_rel_path"
	}
	// raw_text 是 extracted_text 的别名（docproc-web 客户端契约）。
	if req.ExtractedText == "" {
		req.ExtractedText = req.RawText
	}
	if len(req.ExtractedText) > maxExtractedTextBytes {
		return nil, nil, "extracted_text_too_large"
	}
	if req.ContentHashHex != "" {
		h, err := hex.DecodeString(req.ContentHashHex)
		if err != nil {
			return nil, nil, "bad_content_hash"
		}
		contentHash = h
	}
	parseMeta = map[string]any{}
	for k, v := range req.ParseMeta {
		if parseMetaKeys[k] {
			parseMeta[k] = v
		}
	}
	return contentHash, parseMeta, ""
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.requireOwner(w, r, pid) {
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	contentHash, parseMeta, normErr := normalizeCreateReq(&req)
	if normErr != "" {
		status := http.StatusBadRequest
		if normErr == "extracted_text_too_large" {
			status = http.StatusRequestEntityTooLarge
		}
		wikicommon.WriteErr(w, status, normErr, "")
		return
	}
	var fileID *uuid.UUID
	if req.FileID != "" {
		fid, err := uuid.Parse(req.FileID)
		if err != nil {
			wikicommon.WriteErr(w, http.StatusBadRequest, "bad_file_id", err.Error())
			return
		}
		fileID = &fid
	}
	filename := req.Filename
	if filename == "" {
		// rel_path 末段
		for i := len(req.RelPath) - 1; i >= 0; i-- {
			if req.RelPath[i] == '/' {
				filename = req.RelPath[i+1:]
				break
			}
		}
		if filename == "" {
			filename = req.RelPath
		}
	}
	uid := wikicommon.MustUserID(r)
	src, err := s.Store.Upsert(r.Context(), CreateInput{
		ProjectID:     pid,
		UserID:        &uid,
		FileID:        fileID,
		RelPath:       req.RelPath,
		Filename:      filename,
		Mime:          req.Mime,
		ByteSize:      req.ByteSize,
		ContentHash:   contentHash,
		ExtractedText: req.ExtractedText,
		ParseStatus:   req.ParseStatus,
		ExternalID:    req.ExternalID,
		ParseMeta:     parseMeta,
	})
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// Phase 3：upload 带 file_id 的源入库后触发 wiki-parse worker 解析。
	// 只对有文件的 upload 行触发（webclip 入库即 done，不进 parse 队列）。
	// topic/kind 两段式（与 wiki-llm 订阅同 subject 规范；wiki.ingest 发布端
	// 的重复段 bug 已修）。publish 失败只 warn —— 行已落库，tick rescan 兜底。
	// client-docproc：客户端已随请求提交解析文本时不再触发服务端解析。
	if s.Publisher != nil && fileID != nil && req.ExtractedText == "" {
		payload := map[string]any{
			"source_id":  src.ID.String(),
			"project_id": src.ProjectID.String(),
			"owner_id":   uid.String(),
			"kind":       src.Kind,
			"mime":       src.Mime,
			"filename":   src.Filename,
		}
		pubCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.Publisher.Publish(pubCtx, "wiki.parse", "requested", payload); err != nil {
			s.Logger.Warn("wiki parse publish failed",
				"source_id", src.ID, "err", err)
		}
	}
	// client-docproc：客户端本机解析随 source 提交了 extracted_text +
	// content_hash，与 worker parse-result 路径同样查项目内 dedup。
	if req.ExtractedText != "" && len(contentHash) > 0 && s.Reviews != nil {
		DetectSourceDupes(r.Context(), s.Store, s.Reviews, s.Logger, src, contentHash)
	}
	wikicommon.WriteJSON(w, http.StatusCreated, sourceJSON(src))
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.requireOwner(w, r, pid) {
		return
	}
	rows, err := s.Store.ListByProject(r.Context(), pid, 200)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]sourceOut, 0, len(rows))
	for _, src := range rows {
		out = append(out, sourceJSON(src))
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"sources": out})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	src, ok := s.loadOwnedSource(w, r)
	if !ok {
		return
	}
	if err := s.Store.Delete(r.Context(), src.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "source")
			return
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"deleted": src.ID.String()})
}

// handleDeletePreview returns the pages that would lose their only-source
// link if this source were deleted. B4 dedup/cleanup module 会基于此决定
// 是否要 cascade 删页。当前实现返回空数组 —— 等 wiki/api 里 page→source 关联
// 落库后再补真业务（此时源文件还没被 ingest pipeline 关联到 page）。
func (s *Server) handleDeletePreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.loadOwnedSource(w, r); !ok {
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{
		"affected_pages": []any{},
		"note":           "page→source linkage 未落库；B2 ingest pipeline 完整后回填",
	})
}

func (s *Server) handleExternalIDs(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.requireOwner(w, r, pid) {
		return
	}
	ids, err := s.Store.ExternalIDsInProject(r.Context(), pid)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	wikicommon.WriteJSON(w, http.StatusOK, map[string]any{"external_ids": ids})
}

// ─── Auth helpers ───────────────────────────────────────────────

func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request, pid uuid.UUID) bool {
	uid := wikicommon.MustUserID(r)
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "project")
		return false
	}
	if proj.OwnerID != uid {
		wikicommon.WriteErr(w, http.StatusForbidden, "forbidden", "")
		return false
	}
	return true
}

func (s *Server) loadOwnedSource(w http.ResponseWriter, r *http.Request) (*Source, bool) {
	sid, err := uuid.Parse(r.PathValue("sid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_source_id", "")
		return nil, false
	}
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		wikicommon.WriteErr(w, http.StatusBadRequest, "bad_project_id", "")
		return nil, false
	}
	src, err := s.Store.Get(r.Context(), sid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "source")
			return nil, false
		}
		wikicommon.WriteErr(w, http.StatusInternalServerError, "internal", err.Error())
		return nil, false
	}
	if src.ProjectID != pid {
		// path 里的 pid 和 source.project_id 不一致 —— 拒绝跨项目猜 uuid
		wikicommon.WriteErr(w, http.StatusNotFound, "not_found", "source")
		return nil, false
	}
	if !s.requireOwner(w, r, pid) {
		return nil, false
	}
	_ = moduleName
	return src, true
}
