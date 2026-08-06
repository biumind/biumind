// HTTP layer for provider configs.
//
//	GET    /v1/providers              list user's providers (key masked)
//	POST   /v1/providers              create / configure a provider
//	GET    /v1/providers/{id}         get one (key masked by default;
//	                                  ?reveal=1 returns plaintext for
//	                                  client-side direct dispatch)
//	PATCH  /v1/providers/{id}         update fields
//	DELETE /v1/providers/{id}         remove
//
// All endpoints require Bearer JWT and are owner-scoped (cross-tenant
// → 404).
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

type Server struct {
	Store    *Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
	// IdentityBYOK — P3: 拉上游 /models + daemon agent BYOK 时现取用户
	// 凭据(brain 不再存 key). 可空(dev / 未配 IDENTITY_URL), 空时
	// resolveUpstreamCreds 返 ok=false → refresh 跳过.
	IdentityBYOK *IdentityBYOKClient
}

func NewServer(store *Store, v *bauth.Verifier, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{Store: store, Verifier: v, Logger: logger}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET    /v1/providers", s.requireAuth(s.handleList))
	mux.HandleFunc("POST   /v1/providers", s.requireAuth(s.handleCreate))
	mux.HandleFunc("GET    /v1/providers/{id}", s.requireAuth(s.handleGet))
	mux.HandleFunc("PATCH  /v1/providers/{id}", s.requireAuth(s.handleUpdate))
	mux.HandleFunc("DELETE /v1/providers/{id}", s.requireAuth(s.handleDelete))

	// Model management (connectivity /check endpoint removed in P3 —
	// brain no longer holds keys to probe; users validate keys via
	// identity POST /v1/identity/me/api-keys/{provider}/test).
	mux.HandleFunc("GET    /v1/providers/{id}/models",
		s.requireAuth(s.handleListModels))
	mux.HandleFunc("POST   /v1/providers/{id}/models/refresh",
		s.requireAuth(s.handleRefreshModels))
	mux.HandleFunc("PATCH  /v1/providers/{id}/models/{mid}",
		s.requireAuth(s.handleUpdateModel))
	mux.HandleFunc("DELETE /v1/providers/{id}/models/{mid}",
		s.requireAuth(s.handleDeleteModel))
}

// ─── DTOs ────────────────────────────────────────────────

type providerDTO struct {
	ID          string         `json:"id"`
	ProviderID  string         `json:"provider_id"`
	DisplayName string         `json:"display_name"`
	BaseURL     *string        `json:"base_url,omitempty"`
	Enabled     bool           `json:"enabled"`
	Source      string         `json:"source"`
	Config      map[string]any `json:"config"`
	SortOrder   int            `json:"sort_order"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// toDTO projects a Provider for client responses. P3: 不再含 api_key /
// has_api_key / fetch_mode / internal (key 归 identity, 另两列已删).
func toDTO(p *Provider) providerDTO {
	return providerDTO{
		ID:          p.ID.String(),
		ProviderID:  p.ProviderID,
		DisplayName: p.DisplayName,
		BaseURL:     p.BaseURL,
		Enabled:     p.Enabled,
		Source:      p.Source,
		Config:      p.Config,
		SortOrder:   p.SortOrder,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// ─── Handlers ────────────────────────────────────────────

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	rows, err := s.Store.List(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]providerDTO, 0, len(rows))
	for _, p := range rows {
		out = append(out, toDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

type createReq struct {
	ProviderID  string         `json:"provider_id"`
	DisplayName string         `json:"display_name"`
	BaseURL     *string        `json:"base_url"`
	Enabled     *bool          `json:"enabled"`
	Source      string         `json:"source"`
	Config      map[string]any `json:"config"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	p, err := s.Store.Create(r.Context(), CreateInput{
		UserID:      userID(r),
		ProviderID:  req.ProviderID,
		DisplayName: req.DisplayName,
		BaseURL:     req.BaseURL,
		Enabled:     req.Enabled,
		Source:      req.Source,
		Config:      req.Config,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrConflict):
			writeErr(w, http.StatusConflict, "conflict", err.Error())
		case errors.Is(err, ErrInvalid):
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		}
		return
	}
	// 后台异步拉上游 /models —— 让模型列表立即反映真实清单(替换
	// catalog 兜底)。helper 内部对 official / identity 无 key 跳过。
	s.refreshModelsAsync(p)
	writeJSON(w, http.StatusCreated, toDTO(p))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r.PathValue("id"))
	if !ok {
		return
	}
	p, err := s.Store.GetByID(r.Context(), userID(r), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDTO(p))
}

type updateReq struct {
	DisplayName *string        `json:"display_name"`
	BaseURL     *string        `json:"base_url"`
	Enabled     *bool          `json:"enabled"`
	Config      map[string]any `json:"config"`
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req updateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	p, err := s.Store.Update(r.Context(), UpdateInput{
		UserID:      userID(r),
		ID:          id,
		DisplayName: req.DisplayName,
		BaseURL:     req.BaseURL,
		Enabled:     req.Enabled,
		Config:      req.Config,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", "")
		case errors.Is(err, ErrInvalid):
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		}
		return
	}
	// baseUrl 变更 → 后台重拉上游 /models(新端点下清单可能不同)。
	// P3: key 不再经 brain (走 identity /api-keys), 故 key 变更不在此触发。
	if req.BaseURL != nil {
		s.refreshModelsAsync(p)
	}
	writeJSON(w, http.StatusOK, toDTO(p))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.Store.Delete(r.Context(), userID(r), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Models + connectivity ───────────────────────────────

type modelDTO struct {
	ID            string         `json:"id"`
	ProviderID    string         `json:"provider_id"`
	ModelID       string         `json:"model_id"`
	DisplayName   string         `json:"display_name"`
	Type          string         `json:"type"`
	Abilities     map[string]bool `json:"abilities"`
	ContextWindow *int           `json:"context_window,omitempty"`
	Pricing       map[string]any `json:"pricing,omitempty"`
	ReleasedAt    *time.Time     `json:"released_at,omitempty"`
	Enabled       bool           `json:"enabled"`
	SortOrder     int            `json:"sort_order"`
	Source        string         `json:"source"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func modelToDTO(m *Model) modelDTO {
	return modelDTO{
		ID:            m.ID.String(),
		ProviderID:    m.ProviderID,
		ModelID:       m.ModelID,
		DisplayName:   m.DisplayName,
		Type:          m.Type,
		Abilities:     m.Abilities,
		ContextWindow: m.ContextWindow,
		Pricing:       m.Pricing,
		ReleasedAt:    m.ReleasedAt,
		Enabled:       m.Enabled,
		SortOrder:     m.SortOrder,
		Source:        m.Source,
		UpdatedAt:     m.UpdatedAt,
	}
}

// handleListModels — GET /v1/providers/{id}/models[?type=chat]
//
// Side-effect:on first call for a builtin/official provider with no
// stored models yet, lazy-seeds from the static catalog so the UI's
// initial render isn't empty.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r.PathValue("id"))
	if !ok {
		return
	}
	uid := userID(r)
	prov, err := s.Store.GetByID(r.Context(), uid, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	// P6: builtin/official 模型清单改由 client 直读 model-relay /v1/me/models,
	// brain 不再 lazy-seed 静态 catalog 也不批量同步 official。此处仅做
	// per-user 上游 /models 同步 (custom + builtin 有 identity key 时),
	// 让用户进设置页看到自己配的上游真实模型清单。失败只 log;设置页低频
	// + /models 免费,每次 list 拉可接受。P3: key 现从 identity 现取。
	if prov.Source != SourceOfficial {
		if base, apiKey, ok := s.resolveUpstreamCreds(r.Context(), uid, prov); ok {
			fetchCtx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
			rows, ferr := fetchUpstreamModels(fetchCtx, prov.ProviderID, base, apiKey, uid)
			cancel()
			if ferr != nil {
				s.Logger.Debug("list: upstream models fetch failed",
					"provider_id", prov.ProviderID, "err", ferr)
			} else {
				for _, m := range rows {
					_, _ = s.Store.UpsertModel(r.Context(), m)
				}
			}
		}
	}
	typ := r.URL.Query().Get("type")
	rows, err := s.Store.ListModels(r.Context(), ListModelsInput{
		UserID:     uid,
		ProviderID: prov.ProviderID,
		Type:       typ,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]modelDTO, 0, len(rows))
	for _, m := range rows {
		out = append(out, modelToDTO(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// handleRefreshModels — POST /v1/providers/{id}/models/refresh
//
// For builtin providers we re-seed from the static catalog (cheap
// idempotent upsert, used when the catalog ships new models). For
// custom providers we'd hit /models on the upstream — left as a TODO
// since the format varies. For now: catalog-only refresh.
func (s *Server) handleRefreshModels(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r.PathValue("id"))
	if !ok {
		return
	}
	uid := userID(r)
	prov, err := s.Store.GetByID(r.Context(), uid, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	// Official channel: model list is intentionally hidden from users.
	// Just no-op the refresh.
	if prov.Source == SourceOfficial {
		writeJSON(w, http.StatusOK, map[string]any{"refreshed": 0})
		return
	}

	// Try to fetch the real upstream /models endpoint. Each provider
	// has a different shape, but fetchUpstreamModels normalizes them
	// into a uniform ModelInput slice. On upstream failure (or no key
	// in identity), fall back to the static catalog (only useful for
	// builtin providers). P3: key 现从 identity 现取.
	base, apiKey, hasKey := s.resolveUpstreamCreds(r.Context(), uid, prov)
	var rows []ModelInput
	if hasKey {
		rows, err = fetchUpstreamModels(r.Context(), prov.ProviderID, base, apiKey, uid)
	} else {
		err = errors.New("no api key configured in identity")
	}
	if err != nil {
		// P6: 静态 catalog 已删 (global 模型改 client 直读 model-relay),
		// 上游 /models 失败直接报错, 不再 catalog 兜底。
		s.Logger.Warn("upstream model fetch failed",
			"provider_id", prov.ProviderID, "err", err)
		writeErr(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	for _, m := range rows {
		_, _ = s.Store.UpsertModel(r.Context(), m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": len(rows)})
}

type updateModelReq struct {
	Enabled   *bool `json:"enabled"`
	SortOrder *int  `json:"sort_order"`
}

func (s *Server) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	mid, ok := parseUUID(w, r.PathValue("mid"))
	if !ok {
		return
	}
	var req updateModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	m, err := s.Store.UpdateModel(r.Context(), UpdateModelInput{
		UserID:    userID(r),
		ID:        mid,
		Enabled:   req.Enabled,
		SortOrder: req.SortOrder,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, modelToDTO(m))
}

func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	mid, ok := parseUUID(w, r.PathValue("mid"))
	if !ok {
		return
	}
	if err := s.Store.DeleteModel(r.Context(), userID(r), mid); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refreshModelsAsync 在后台拉上游 /models 并 upsert,用于 handleCreate /
// handleUpdate 后让模型列表立即反映真实上游(替换写死的 catalog 兜底)。
// 失败只 log warn 不阻断 —— 用户仍看 catalog + 手动刷新入口。
//
// 复用 refresh.go 的 fetchUpstreamModels(OpenAI/Anthropic/Google adapter),
// 不重写。P3: key 不再从 *Provider 读, 改 resolveUpstreamCreds 现取 identity.
// goroutine 用独立 ctx(不绑请求生命周期,请求返回后仍跑完)。
func (s *Server) refreshModelsAsync(p *Provider) {
	if p == nil || s.Store == nil {
		return
	}
	if p.Source == SourceOfficial {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		base, apiKey, ok := s.resolveUpstreamCreds(ctx, p.UserID, p)
		if !ok {
			// identity 没该 provider 的 key — 跳过, 留 catalog / 手动刷新。
			return
		}
		rows, err := fetchUpstreamModels(ctx, p.ProviderID, base, apiKey, p.UserID)
		if err != nil {
			s.Logger.Warn("auto-refresh upstream models failed",
				"provider_id", p.ProviderID, "user_id", p.UserID, "err", err)
			return
		}
		for _, m := range rows {
			if _, err := s.Store.UpsertModel(ctx, m); err != nil {
				s.Logger.Warn("auto-refresh upsert model failed",
					"provider_id", p.ProviderID, "model_id", m.ModelID, "err", err)
			}
		}
		s.Logger.Info("auto-refreshed upstream models",
			"provider_id", p.ProviderID, "user_id", p.UserID, "count", len(rows))
	}()
}

// resolveUpstreamCreds 现取用户某 provider 的上游凭据 (P3: brain 不存 key).
// 返 (base, apiKey, ok). ok=false = identity 无该 provider 的有效 key (或
// client 未配) → 调用方应跳过上游 refresh, 走 catalog 兜底. base 优先用
// identity 的 base_url (与 model-relay 实际调用一致), 回退 provider 行的
// base_url (brain chat.providers.base_url 仍存).
func (s *Server) resolveUpstreamCreds(ctx context.Context, userID uuid.UUID, prov *Provider) (base, apiKey string, ok bool) {
	if s.IdentityBYOK == nil {
		return "", "", false
	}
	key, err := s.IdentityBYOK.Get(ctx, userID, prov.ProviderID)
	if err != nil || key == nil {
		return "", "", false
	}
	base = key.BaseURL
	if base == "" && prov.BaseURL != nil {
		base = *prov.BaseURL
	}
	return base, key.APIKey, true
}

// truncate keeps long upstream errors readable in API responses.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ─── Auth + helpers ──────────────────────────────────────

type ctxKey int

const userKey ctxKey = 0

func userID(r *http.Request) uuid.UUID {
	if v, ok := r.Context().Value(userKey).(uuid.UUID); ok {
		return v
	}
	return uuid.Nil
}

func ctxWithUser(ctx context.Context, uid uuid.UUID) context.Context {
	return context.WithValue(ctx, userKey, uid)
}

func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Verifier == nil {
			writeErr(w, http.StatusUnauthorized, "no_auth", "verifier missing")
			return
		}
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
		uid, err := uuid.Parse(claims.UserID)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "bad_subject", err.Error())
			return
		}
		h(w, r.WithContext(ctxWithUser(r.Context(), uid)))
	}
}

func parseUUID(w http.ResponseWriter, raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "invalid uuid")
		return uuid.Nil, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}
