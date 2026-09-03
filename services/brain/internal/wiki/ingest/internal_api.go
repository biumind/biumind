// Internal endpoint consumed by workers/wiki-llm to resolve
// source_id-only ingest tasks. The worker calls this when it sees a
// task with no inline raw_text and a non-nil source_id.
//
// Auth: a single shared secret in the X-Biumind-Internal-Token header
// (env BIUMIND_INTERNAL_TOKEN on both sides). Constant-time comparison
// prevents timing oracles. The token is service-to-service — it MUST
// NOT be exposed to end users; rotate via env redeploy when leaked.
//
// Why not JWT: a JWT would force brain to either (a) embed signing
// keys it doesn't otherwise need or (b) call identity to mint per-task
// tokens. Both add moving parts for a single-purpose private endpoint
// where the trust boundary is "is the caller a known internal pod?".
// The shared-secret check is the right scope for that.
//
// Cross-validation: even with the right token the worker may only
// fetch a source whose owner_id matches the task's owner_id passed in
// the same call. This guards against a leaked token being used to
// scrape arbitrary sources — the worker also has to know a valid
// (task_id, owner_id) pairing, which is bounded by NATS payload
// reach.
package ingest

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/services/brain/internal/files"
	"github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	"github.com/biumind/biumind/services/brain/internal/wiki/sources"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// InternalServer mounts the X-Biumind-Internal-Token gated routes.
// Separate from Server so the public API surface stays clean (a single
// Mount(mux) call doesn't accidentally expose internal routes).
type InternalServer struct {
	Sources *sources.Store
	Blob    *files.Blob    // Phase 3：presign 给 wiki-parse 下载文件；nil → blob-presign 503
	Reviews *reviews.Store // Phase 3：parse done 后查项目内 source dedup；nil → 跳过
	Charger *UsageCharger  // W4：云端解析按页扣费（经 model-relay）；nil → 跳过
	// B1 OCR：parser=mineru 的解析走独立计费档位（wiki-ocr pseudo-model）；
	// nil → OCR 免费兜底（与 Charger 同哲学：计费缺配不阻塞解析）。
	OCRCharger *UsageCharger
	// Wiki：P2 #17 两阶段 ingest 的上下文端点（purpose/schema 正文 +
	// 页面索引）；nil → ingest-context 503，worker 降级为空上下文。
	Wiki   *wikistore.Store
	Token  string
	Logger *slog.Logger
}

func NewInternalServer(s *sources.Store, token string, l *slog.Logger) *InternalServer {
	return &InternalServer{Sources: s, Token: token, Logger: l}
}

// Mount registers the internal routes. Mount is a no-op when the token
// is empty so a misconfigured deploy can't accidentally serve open
// internal endpoints — the worker will report "internal endpoint not
// reachable" instead, which surfaces the configuration gap loudly.
func (s *InternalServer) Mount(mux *http.ServeMux) {
	if s.Token == "" {
		s.Logger.Warn("wiki ingest internal endpoint disabled (token empty)")
		return
	}
	mux.HandleFunc("GET /v1/internal/wiki/sources/parse-queue", s.requireToken(s.handleParseQueue))
	mux.HandleFunc("GET /v1/internal/wiki/sources/{id}", s.requireToken(s.handleGetSource))
	mux.HandleFunc("GET /v1/internal/wiki/sources/{id}/blob-presign", s.requireToken(s.handleBlobPresign))
	mux.HandleFunc("POST /v1/internal/wiki/sources/{id}/parse-result", s.requireToken(s.handleParseResult))
	mux.HandleFunc("GET /v1/internal/wiki/projects/{pid}/ingest-context", s.requireToken(s.handleIngestContext))
}

// handleGetSource returns the source row identified by `id`. Required
// query param: `owner_id` — the worker passes the task's owner_id and
// brain refuses to return a source whose user_id doesn't match. This
// is defence in depth: the token alone could be enough for a leak; the
// owner_id pairing scopes each call to a known caller context.
func (s *InternalServer) handleGetSource(w http.ResponseWriter, r *http.Request) {
	sid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return
	}
	ownerStr := r.URL.Query().Get("owner_id")
	owner, err := uuid.Parse(ownerStr)
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, "bad_owner_id", "owner_id required")
		return
	}
	src, err := s.Sources.GetByID(r.Context(), sid)
	if err != nil {
		if errors.Is(err, sources.ErrNotFound) {
			writeInternalErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeInternalErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// src.UserID 现是 *uuid.UUID（upload 行可能 NULL）。owner 不匹配或
	// 行无归属 → 404 不泄存在（与旧行为一致，防 scrape foreign tenant）。
	if src.UserID == nil || *src.UserID != owner {
		writeInternalErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	// 响应保留旧字段别名（raw/sha256/status）兼容 Python worker ——
	// brain.py:53,67 SourceRow 只解析 raw+title 两字段。Phase 2 worker
	// 改读 extracted_text/content_hash/parse_status 后再去别名。
	ownerID := ""
	if src.UserID != nil {
		ownerID = src.UserID.String()
	}
	writeInternalJSON(w, http.StatusOK, map[string]any{
		"id":             src.ID.String(),
		"project_id":     src.ProjectID.String(),
		"owner_id":       ownerID,
		"kind":           src.Kind,
		"url":            src.URL,
		"title":          src.Title,
		"raw":            src.ExtractedText, // 别名：webclip 正文（旧 raw）
		"extracted_text": src.ExtractedText,
		"metadata":       src.Metadata,
		"sha256":         hex.EncodeToString(src.ContentHash), // 别名
		"content_hash":   hex.EncodeToString(src.ContentHash),
		"status":         src.ParseStatus, // 别名
		"parse_status":   src.ParseStatus,
		"created_at":     src.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// requireToken does a constant-time match against the configured shared
// secret. Length-mismatch tokens are constant-time-rejected by padding
// the comparison to a fixed length so timing doesn't leak the prefix.
func (s *InternalServer) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Biumind-Internal-Token")
		// subtle.ConstantTimeCompare returns 0 on length mismatch; we
		// still want both branches to do equal work to avoid leaking the
		// real token's length. Comparing two byte slices of identical
		// length (1) achieves that when one is the wrong length.
		want := []byte(s.Token)
		gotB := []byte(got)
		ok := len(want) == len(gotB) &&
			subtle.ConstantTimeCompare(want, gotB) == 1
		// Burn one extra ConstantTimeCompare on a fixed-length pair so
		// the rejected-length and rejected-content paths take the same
		// time. Cheap (len 1).
		_ = subtle.ConstantTimeCompare([]byte("x"), []byte("x"))
		if !ok {
			writeInternalErr(w, http.StatusUnauthorized, "bad_token", "")
			return
		}
		next(w, r)
	}
}

// ─── helpers ───────────────────────────────────────────────────

func writeInternalJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeInternalErr(w http.ResponseWriter, status int, code, msg string) {
	writeInternalJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

// ─── Phase 3: wiki-parse worker 端点 ────────────────────────────
//
// 三个端点合成 parser 闭环：
//
//	GET  /v1/internal/wiki/sources/parse-queue       worker 启动/tick 拉 queued 行
//	GET  /v1/internal/wiki/sources/{id}/blob-presign 取文件 presigned URL 下载
//	POST /v1/internal/wiki/sources/{id}/parse-result 回写 extracted_text/hash/status + dedup
//
// 全部 requireToken（service-to-service shared secret）+ owner_id 配对
// （parse-queue 例外：跨 owner 返最小元数据，token 鉴权已够）。

// blobPresignTTL — presigned GET URL 有效期。wiki-parse worker 内网下载，
// 15min 足够 200MB 文件；与 files/api_presign.go presignGetTTL 对齐。
const blobPresignTTL = 15 * time.Minute

// loadOwnedSource 取 source 行并做 owner 配对（owner_id query 必传）。
// 复用 handleGetSource 的防 scrape 语义：owner 不匹配或行无归属 → 404 不泄存在。
func (s *InternalServer) loadOwnedSource(w http.ResponseWriter, r *http.Request) (*sources.Source, bool) {
	sid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, "bad_id", err.Error())
		return nil, false
	}
	ownerStr := r.URL.Query().Get("owner_id")
	owner, err := uuid.Parse(ownerStr)
	if err != nil {
		writeInternalErr(w, http.StatusBadRequest, "bad_owner_id", "owner_id required")
		return nil, false
	}
	src, err := s.Sources.GetByID(r.Context(), sid)
	if err != nil {
		if errors.Is(err, sources.ErrNotFound) {
			writeInternalErr(w, http.StatusNotFound, "not_found", "")
			return nil, false
		}
		writeInternalErr(w, http.StatusInternalServerError, "internal", err.Error())
		return nil, false
	}
	if src.UserID == nil || *src.UserID != owner {
		writeInternalErr(w, http.StatusNotFound, "not_found", "")
		return nil, false
	}
	return src, true
}

// handleBlobPresign mints a presigned GET URL for the source's file blob.
// Worker downloads via httpx; never sees MinIO credentials.
func (s *InternalServer) handleBlobPresign(w http.ResponseWriter, r *http.Request) {
	src, ok := s.loadOwnedSource(w, r)
	if !ok {
		return
	}
	if src.FileID == nil {
		writeInternalErr(w, http.StatusBadRequest, "no_file", "source has no file_id")
		return
	}
	if s.Blob == nil {
		writeInternalErr(w, http.StatusServiceUnavailable, "minio_not_configured", "")
		return
	}
	objectKey, err := s.Sources.FileObjectKey(r.Context(), src.ID)
	if err != nil {
		if errors.Is(err, sources.ErrNotFound) {
			writeInternalErr(w, http.StatusNotFound, "not_found", "file object")
			return
		}
		writeInternalErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	pctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	signed, err := s.Blob.PresignGet(pctx, objectKey, blobPresignTTL)
	if err != nil {
		writeInternalErr(w, http.StatusInternalServerError, "presign_failed", err.Error())
		return
	}
	writeInternalJSON(w, http.StatusOK, map[string]any{
		"url":        signed.String(),
		"expires_at": time.Now().Add(blobPresignTTL).UTC().Format(time.RFC3339),
		"filename":   src.Filename,
		"mime":       src.Mime,
		"byte_size":  src.ByteSize,
	})
}

type parseResultReq struct {
	ExtractedText string `json:"extracted_text"`
	ContentHash   string `json:"content_hash"` // hex（sha256(extracted_text)）
	ParseStatus   string `json:"parse_status"` // done | error
	ParseError    string `json:"parse_error,omitempty"`
	PageCount     int    `json:"page_count,omitempty"` // W4 计费依据（PDF 页数；0 = 未知）
	Parser        string `json:"parser,omitempty"`     // B1 OCR：pypdf | mineru；空 = 旧 worker 未上报
}

// terminalParseErrorPrefix 是 worker（wiki-parse）约定的终态错误前缀：
// 不可重试的失败（4xx / 文件损坏）。brain 收到后把 retries 直接置上限
// （ParseMaxRetries），ListParseQueue 不再重扫 —— 防终态错误反复烧
// MinerU 算力（原来要重扫 3 次才停，现在 1 次即止）。
const terminalParseErrorPrefix = "[terminal]"

// handleParseResult 是 worker 解析完回写入口。done 回写 extracted_text +
// content_hash + parse_status=done；error 回写 parse_error + retries++
// （parse_error 带 [terminal] 前缀 = 终态错误，retries 直接置上限不再重扫）。
// done 且有 content_hash 时同步做项目内 source dedup 检测 → review_items。
func (s *InternalServer) handleParseResult(w http.ResponseWriter, r *http.Request) {
	src, ok := s.loadOwnedSource(w, r)
	if !ok {
		return
	}
	var req parseResultReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInternalErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.ParseStatus != "done" && req.ParseStatus != "error" {
		writeInternalErr(w, http.StatusBadRequest, "bad_parse_status",
			"parse_status must be done|error")
		return
	}
	var contentHash []byte
	if req.ContentHash != "" {
		h, err := hex.DecodeString(req.ContentHash)
		if err != nil {
			writeInternalErr(w, http.StatusBadRequest, "bad_content_hash", "expect hex")
			return
		}
		contentHash = h
	}
	terminal := req.ParseStatus == "error" &&
		strings.HasPrefix(req.ParseError, terminalParseErrorPrefix)
	updated, err := s.Sources.UpdateParseStatus(r.Context(), sources.UpdateParseInput{
		ID:            src.ID,
		ParseStatus:   req.ParseStatus,
		ExtractedText: req.ExtractedText,
		ContentHash:   contentHash,
		ParseError:    req.ParseError,
		BumpRetries:   req.ParseStatus == "error" && !terminal,
		TerminalError: terminal,
		PageCount:     req.PageCount,
		Parser:        req.Parser,
	})
	if err != nil {
		if errors.Is(err, sources.ErrNotFound) {
			writeInternalErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeInternalErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	// dedup 仅在 done + 有 hash + Reviews 注入时跑。失败只 warn —— parse 已成功，
	// dedup 漏检不阻塞主路径（下个 tick 不会重跑 done 行，但人工 review 可补）。
	if req.ParseStatus == "done" && len(contentHash) > 0 && s.Reviews != nil {
		sources.DetectSourceDupes(r.Context(), s.Sources, s.Reviews, s.Logger, updated, contentHash)
	}
	// W4 云端解析计费：done + 有页数 + charger 已配 → 经 model-relay 按页
	// 扣费（后付费，幂等键 parse:<source_id>；失败只记日志不阻塞）。
	// B1 分档：parser=mineru 走 OCR charger；OCRCharger nil = OCR 免费兜底。
	charger := s.Charger
	if req.Parser == "mineru" {
		charger = s.OCRCharger
	}
	if req.ParseStatus == "done" && req.PageCount > 0 && charger != nil && updated.UserID != nil {
		charger.ChargeParse(r.Context(), updated.UserID.String(), src.ID.String(), int64(req.PageCount))
	}
	writeInternalJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"parse_status": updated.ParseStatus,
		"retries":      "retries_obscured",
	})
}

// handleParseQueue returns the upload rows awaiting parse (queued or
// errored with retries left). Worker calls this on startup + each tick
// to backstop the NATS trigger. Returns minimal metadata only — no
// extracted_text/raw — so a leaked token can't scrape file contents.
// Owner scoping happens per-source on the subsequent blob-presign +
// parse-result calls (which require owner_id pairing).
func (s *InternalServer) handleParseQueue(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Sources.ListParseQueue(r.Context(), sources.ParseMaxRetries, 100)
	if err != nil {
		writeInternalErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, src := range rows {
		ownerID := ""
		if src.UserID != nil {
			ownerID = src.UserID.String()
		}
		out = append(out, map[string]any{
			"source_id":  src.ID.String(),
			"project_id": src.ProjectID.String(),
			"owner_id":   ownerID,
			"kind":       src.Kind,
			"mime":       src.Mime,
			"filename":   src.Filename,
		})
	}
	writeInternalJSON(w, http.StatusOK, map[string]any{"sources": out})
}
