package api

// settings.go — per-user 设置的用户侧公开 endpoint（B2 ingest 模型偏好）.
//
//   GET /v1/identity/me/settings/ingest-model   (Bearer)  → {"model":"..."} 或 {"model":""}
//   PUT /v1/identity/me/settings/ingest-model   (Bearer)  body {"model":"..."}；空串 = 清除偏好
//
// 鉴权: 过 requireAuth, 操作仅作用于当前用户。存储见 internal/settings
// （identity.user_settings 通用 KV, migration 00020）。
//
// 模型校验只做到字符集 / 长度层面（信任客户端, UI 用 catalog 约束可选值）;
// 不做到 model-relay 的存活校验 —— 偏好只是一个名字, 真正调用时才路由。
//
// worker 按 owner 拉取走 internalapi 的
// GET /v1/internal/settings/{user_id}/ingest-model（未设置 404, 消费方回落）。

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/identity/internal/settings"
)

// ingestModelMaxLen — 模型 code 长度上限（catalog 里最长的名字远短于此,
// 200 给未来 custom 模型留余量, 同时挡住明显异常输入）.
const ingestModelMaxLen = 200

// MountSettings 把 settings 路由挂到 mux. 在 Mount 中调用一次.
// store=nil 时跳过（与 MountAPIKeys 同形态）.
func (s *Server) MountSettings(mux *http.ServeMux) {
	if s.Settings == nil {
		return
	}
	mux.HandleFunc("GET /v1/identity/me/settings/ingest-model",
		s.requireAuth(s.handleGetIngestModel))
	mux.HandleFunc("PUT /v1/identity/me/settings/ingest-model",
		s.requireAuth(s.handlePutIngestModel))
}

// ─── GET ──────────────────────────────────────────────

func (s *Server) handleGetIngestModel(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	model, err := s.Settings.GetIngestModel(r.Context(), uid)
	if errors.Is(err, settings.ErrNotFound) {
		// 未设置不是错误 — 返空串让客户端走默认模型.
		writeJSON(w, http.StatusOK, map[string]any{"model": ""})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": model})
}

// ─── PUT ──────────────────────────────────────────────

type putIngestModelReq struct {
	Model string `json:"model"`
}

func (s *Server) handlePutIngestModel(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}

	var body putIngestModelReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}

	// 空串 = 清除偏好（删行, 幂等）.
	if body.Model == "" {
		if err := s.Settings.DeleteIngestModel(r.Context(), uid); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"model": ""})
		return
	}

	if err := validateIngestModel(body.Model); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_model", err.Error())
		return
	}
	if err := s.Settings.SetIngestModel(r.Context(), uid, body.Model); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": body.Model})
}

// validateIngestModel — 模型 code 字符集校验：字母数字 + .-_:/（覆盖
// provider/model 全形态, 如 "anthropic/claude-sonnet-4-6"、"gpt-4o:2024-08-06"）.
func validateIngestModel(model string) error {
	if len(model) > ingestModelMaxLen {
		return errors.New("model name too long (max 200)")
	}
	for _, c := range model {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_', c == ':', c == '/':
		default:
			return errors.New("model name contains illegal character")
		}
	}
	return nil
}
