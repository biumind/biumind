package api

// api_keys.go — BYOK (Bring Your Own Key) 用户侧公开 endpoint.
//
//   GET    /v1/identity/me/api-keys                         列表 (last4 + 状态, 不返明文)
//   PUT    /v1/identity/me/api-keys/{provider}              新建 / 覆盖一把 Key
//   DELETE /v1/identity/me/api-keys/{provider}              撤销
//   POST   /v1/identity/me/api-keys/{provider}/test         主动 ping 上游
//
// 鉴权: 所有路由都过 requireAuth, 操作仅作用于当前用户.
//
// 上传 PUT 后异步触发一次 validator.Ping (不阻塞响应). test 则是同步.
//
// 设计: docs/BiuMind-Billing-Redesign.md §5.4.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/biumind/biumind/services/identity/internal/byok"
	"github.com/google/uuid"
)

// MountAPIKeys 把 BYOK 路由挂到 mux. 在 Mount 中调用一次.
func (s *Server) MountAPIKeys(mux *http.ServeMux) {
	if s.BYOK == nil {
		return
	}
	mux.HandleFunc("GET /v1/identity/me/api-keys",
		s.requireAuth(s.handleListAPIKeys))
	mux.HandleFunc("GET /v1/identity/me/api-keys/{id}/credentials",
		s.requireAuth(s.handleGetAPIKeyCredentials))
	mux.HandleFunc("PUT /v1/identity/me/api-keys/{provider}",
		s.requireAuth(s.handleUpsertAPIKey))
	mux.HandleFunc("DELETE /v1/identity/me/api-keys/{provider}",
		s.requireAuth(s.handleDeleteAPIKey))
	mux.HandleFunc("POST /v1/identity/me/api-keys/{provider}/test",
		s.requireAuth(s.handleTestAPIKey))
}

// ─── GET ──────────────────────────────────────────────

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	list, err := s.BYOK.ListPublic(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, publicEntryJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// ─── PUT ──────────────────────────────────────────────

type upsertAPIKeyReq struct {
	APIKey       string          `json:"api_key"`
	Label        string          `json:"label,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
	BaseURL      string          `json:"base_url,omitempty"`    // 00033: custom 必填
	Protocol     string          `json:"protocol,omitempty"`    // 00033: custom 必填
	ModelGlobs   []string        `json:"model_globs,omitempty"` // 00034: custom 必填
	IsClientSide bool            `json:"is_client_side,omitempty"` // 00035: client-side BYOK (需本机出口, key 仍加密存 identity)
}

func (s *Server) handleUpsertAPIKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	provider := r.PathValue("provider")
	if provider == "" {
		writeErr(w, http.StatusBadRequest, "missing_provider", "")
		return
	}

	var body upsertAPIKeyReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}

	entry, err := s.BYOK.Upsert(r.Context(), byok.UpsertArgs{
		UserID:       uid,
		Provider:     provider,
		Plaintext:    body.APIKey,
		Label:        body.Label,
		ConfigJSON:   body.Config,
		BaseURL:      body.BaseURL,
		Protocol:     body.Protocol,
		ModelGlobs:   body.ModelGlobs,
		IsClientSide: body.IsClientSide,
	})
	if err != nil {
		writeBYOKErr(w, err)
		return
	}

	// 异步 ping 上游 (不阻塞客户端响应). 跳过两类: ① client-side (需本机出口,
	// 上游是内网 proxy, 云端 identity 连不到, ping 无意义 — 连通性由端侧
	// direct_llm_probe 测); ② 编辑不改 key (Plaintext 空, 无 key 可 ping).
	if !body.IsClientSide && body.APIKey != "" && s.BYOKValidator != nil {
		go s.asyncValidate(uid, provider, byok.PingArgs{
			Provider:   provider,
			APIKey:     body.APIKey,
			ConfigJSON: body.Config,
			BaseURL:    body.BaseURL,
			Protocol:   body.Protocol,
		})
	}

	writeJSON(w, http.StatusOK, publicEntryJSON(entry))
}

// asyncValidate — 后台 ping; ping 完直接 mark store. 用 detached context
// 避免请求结束 cancel 把 ping 也断了.
func (s *Server) asyncValidate(userID uuid.UUID, provider string, args byok.PingArgs) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := s.BYOKValidator.Ping(ctx, args)
	switch result {
	case byok.PingValid:
		_ = s.BYOK.MarkValidated(ctx, userID, provider, true)
	case byok.PingInvalid:
		_ = s.BYOK.MarkValidated(ctx, userID, provider, false)
	default:
		// PingNetwork / PingUnknown — 不改 status (新上传默认 valid)
	}
}

// ─── DELETE ───────────────────────────────────────────

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	provider := r.PathValue("provider")
	// query ?client_side=true/false 精确删 server/client 行 (方案 I 同 provider 双行).
	clientSide := r.URL.Query().Get("client_side") == "true"
	// query ?id=<recordId> 精确删单条 (custom client-side 多 base_url 场景:
	// 同 provider 可多行, 必须按 id 删才不误伤其余). 缺省/解析失败 = nil (退原
	// 批删行为, 兼容 server-side standard 单行场景).
	var idPtr *uuid.UUID
	if raw := r.URL.Query().Get("id"); raw != "" {
		if parsed, err := uuid.Parse(raw); err == nil {
			idPtr = &parsed
		}
	}
	if err := s.BYOK.Revoke(r.Context(), uid, provider, clientSide, idPtr); err != nil {
		writeBYOKErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── POST test ────────────────────────────────────────

func (s *Server) handleTestAPIKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	provider := r.PathValue("provider")

	// 取出明文 + config + base_url/protocol (内部走 store.GetDecrypted).
	// client-side BYOK (is_client_side=true) 被 store 过滤 → ErrKeyNotFound → 404,
	// 期望行为: client-side 连通性检测在客户端本机做 (direct_llm_probe), 不经此端点.
	plaintext, cfg, baseURL, protocol, err := s.BYOK.GetDecrypted(r.Context(), uid, provider)
	if errors.Is(err, byok.ErrKeyNotFound) {
		writeErr(w, http.StatusNotFound, "key_not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "decrypt", err.Error())
		return
	}

	if s.BYOKValidator == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"result": string(byok.PingUnknown),
			"reason": "validator not wired",
		})
		return
	}
	result := s.BYOKValidator.Ping(r.Context(), byok.PingArgs{
		Provider:   provider,
		APIKey:     plaintext,
		ConfigJSON: cfg,
		BaseURL:    baseURL,
		Protocol:   protocol,
	})

	// 同步把结果写回 store (test 跟 validate 一样)
	switch result {
	case byok.PingValid:
		_ = s.BYOK.MarkValidated(r.Context(), uid, provider, true)
	case byok.PingInvalid:
		_ = s.BYOK.MarkValidated(r.Context(), uid, provider, false)
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": string(result)})
}

// ─── GET credentials (client-side 明文, daemon / 端侧 _test 取 key) ─────

// handleGetAPIKeyCredentials — 按 record id 取 client-side 凭据明文 (仅
// is_client_side=true 行). 供桌面 daemon (user JWT) 取 key 本机直连 + 端侧
// _test 临时取 key 测连通. server BYOK 行 (is_client_side=false) 走 internal
// token 路径, 此端点返 404 (owner-scoped + 仅 client-side, 避免双鉴权取 key).
func (s *Server) handleGetAPIKeyCredentials(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.requireUserID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	plaintext, cfg, baseURL, protocol, modelGlobs, err := s.BYOK.GetDecryptedByID(r.Context(), uid, id)
	if errors.Is(err, byok.ErrKeyNotFound) {
		writeErr(w, http.StatusNotFound, "key_not_found", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "decrypt", err.Error())
		return
	}
	out := map[string]any{
		"api_key": plaintext,
		"config":  json.RawMessage(cfg),
	}
	if baseURL != "" {
		out["base_url"] = baseURL
	}
	if protocol != "" {
		out["protocol"] = protocol
	}
	if len(modelGlobs) > 0 {
		out["model_globs"] = modelGlobs
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── helpers ──────────────────────────────────────────

func publicEntryJSON(e *byok.PublicEntry) map[string]any {
	out := map[string]any{
		"id":             e.ID,
		"provider":       e.Provider,
		"label":          e.Label,
		"last4":          e.Last4,
		"status":         e.Status,
		"failure_count":  e.FailureCount,
		"created_at":     e.CreatedAt,
		"updated_at":     e.UpdatedAt,
		"is_client_side": e.IsClientSide,
	}
	if e.BaseURL != "" {
		out["base_url"] = e.BaseURL
	}
	if e.Protocol != "" {
		out["protocol"] = e.Protocol
	}
	if len(e.ModelGlobs) > 0 {
		out["model_globs"] = e.ModelGlobs
	}
	if len(e.ConfigJSON) > 0 {
		out["config"] = json.RawMessage(e.ConfigJSON)
	}
	if e.LastValidatedAt != nil {
		out["last_validated_at"] = e.LastValidatedAt
	}
	if e.LastUsedAt != nil {
		out["last_used_at"] = e.LastUsedAt
	}
	return out
}

func writeBYOKErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, byok.ErrInvalidProvider):
		writeErr(w, http.StatusBadRequest, "invalid_provider", err.Error())
	case errors.Is(err, byok.ErrEmptyPlaintext):
		writeErr(w, http.StatusBadRequest, "empty_api_key", "")
	case errors.Is(err, byok.ErrCustomRequiresEndpoint):
		writeErr(w, http.StatusBadRequest, "custom_requires_endpoint", err.Error())
	case errors.Is(err, byok.ErrCustomRequiresModels):
		writeErr(w, http.StatusBadRequest, "custom_requires_models", err.Error())
	case errors.Is(err, byok.ErrKeyNotFound):
		writeErr(w, http.StatusNotFound, "key_not_found", "")
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}
