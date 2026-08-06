// Package api exposes the tool catalog + invocation proxy over HTTP.
//
// 设计文档: docs/BiuMind-Chat-Optimization-Design.md §4.6.
//
// Endpoints:
//
//	GET  /v1/tools                — list tools available in the
//	                                 caller's execution mode.
//	                                 ?execution_mode=cloud|client
//	                                 (defaults to cloud).
//	POST /v1/tools/invoke         — { "name": "wiki.search",
//	                                  "input": {...} }
//	                                 Returns the tool result wrapped in
//	                                 { "name", "result", "duration_ms" }.
//
// Why proxy invoke instead of letting the client call brain.search /
// brain.memory APIs directly:
//   - Tool inputs / outputs are an LLM-facing contract. Anything we
//     ship to the client now becomes part of the model's expected
//     shape; tying it to internal API URLs leaks too much.
//   - Auth + rate-limit can be enforced once at this boundary.
//   - When W7's Flutter ToolHost calls a cloud-only tool (e.g.
//     wiki.search), the same envelope works for both server-side
//     agent loops and client-side ones.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// Server wires the tool registry to the HTTP surface. Verifier is
// shared with chat / wiki / memory so tokens look identical.
type Server struct {
	Registry *tools.Registry
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func New(reg *tools.Registry, v *bauth.Verifier, logger *slog.Logger) *Server {
	return &Server{Registry: reg, Verifier: v, Logger: logger}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET  /v1/tools", s.requireAuth(s.handleList))
	mux.HandleFunc("POST /v1/tools/invoke", s.requireAuth(s.handleInvoke))
}

// ─── Handlers ─────────────────────────────────────────────────

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	mode := tools.ExecutionMode(r.URL.Query().Get("execution_mode"))
	if mode == "" {
		mode = tools.ExecutionCloud
	}
	if !tools.ValidExecutionMode(string(mode)) {
		writeErr(w, http.StatusBadRequest, "bad_mode",
			"execution_mode must be cloud|client")
		return
	}
	descriptors := s.Registry.Available(mode)
	writeJSON(w, http.StatusOK, map[string]any{
		"execution_mode": string(mode),
		"tools":          descriptors,
	})
}

type invokeReq struct {
	Name          string          `json:"name"`
	Input         json.RawMessage `json:"input"`
	ExecutionMode string          `json:"execution_mode"`
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request) {
	var req invokeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "missing_name", "")
		return
	}
	mode := tools.ExecutionMode(req.ExecutionMode)
	if mode == "" {
		// Default cloud — calling the proxy is by definition a server
		// invocation. Client mode is meaningful only for ToolHost
		// dispatch on the client side; if a client passes mode=client
		// here it means "give me results as if I'd run it locally,
		// but from the server" which is the cross-runtime use-case
		// (W7: client agent reaches for a cloud-only tool through
		// this proxy).
		mode = tools.ExecutionCloud
	}
	if !tools.ValidExecutionMode(string(mode)) {
		writeErr(w, http.StatusBadRequest, "bad_mode",
			"execution_mode must be cloud|client")
		return
	}

	// Inject caller identity into ctx so owner-scoped tools (wiki,
	// memory, ...) get the right user without depending on tool input.
	uid := mustUserID(r)
	ctx := tools.WithUserID(r.Context(), uid)

	start := time.Now()
	result, err := s.Registry.Invoke(ctx, mode, req.Name, req.Input)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		switch {
		case errors.Is(err, tools.ErrUnknownTool):
			writeErr(w, http.StatusNotFound, "unknown_tool", err.Error())
		case errors.Is(err, tools.ErrInvalidRT):
			writeErr(w, http.StatusForbidden, "wrong_runtime", err.Error())
		case errors.Is(err, tools.ErrNotInvocable):
			// Descriptor-only tool — server has no implementation,
			// caller must run it locally on the client side.
			writeErr(w, http.StatusBadRequest, "not_invocable", err.Error())
		default:
			s.Logger.Warn("tool invoke failed",
				"name", req.Name, "user", uid.String(), "err", err)
			writeErr(w, http.StatusInternalServerError, "invoke_failed",
				err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        req.Name,
		"result":      result,
		"duration_ms": dur,
	})
}

// ─── Auth ─────────────────────────────────────────────────────

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

// ─── helpers ───────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error":   code,
		"message": msg,
	})
}
