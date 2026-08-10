// Package internalapi exposes service-to-service endpoints on model-relay.
//
// These are NOT admin endpoints (those live in adminapi/) and NOT public
// /v1/messages traffic — they are inter-service calls from sibling
// BiuMind services (currently aigc) that need to resolve platform-level
// credentials without re-implementing envelope encryption.
//
// Authentication: shared bearer token (IDENTITY_INTERNAL_TOKEN env, same
// value identity / model-relay use). NetworkPolicy restricts these
// paths to in-cluster pods; the token is defence-in-depth.
//
// Currently exposes:
//
//	POST /v1/internal/credentials/{id}/get-decrypted
//	     → {"api_key":"...", "base_url":"...", "headers":{...}}
//
// SECURITY:
//   - Plaintext key flows through this handler exactly once per call;
//     not logged, not in trace attributes, not in error messages.
//   - The handler returns 503 if Vault is nil so misconfig is loud.
//   - Token mismatch returns 401 with constant-time compare to defeat
//     timing attacks on the shared secret.
package internalapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/api"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// Server bundles the internal handlers + their dependencies.
type Server struct {
	// Token is the shared bearer expected on every request. Empty
	// disables auth entirely — only acceptable in tests.
	Token string

	// Vault decrypts envelope-stored credentials. Required for any
	// credentials endpoint to function; nil → 503.
	Vault *registry.CredentialVault

	// Images / Videos — 段 3.6: /v1/internal/generations 复用对外
	// image/video handler 执行实际生成(经 model-relay 单一 egress)。
	// nil 时该端点返 503。
	Images *api.ImagesHandler
	Videos *api.VideosHandler

	// Messages / Transcriptions — 爆款解析(hotparse):aigc worker 经
	// /v1/internal/chat 调 LLM 拆解、经 /v1/internal/transcribe 调 STT
	// (I6 单一 egress,复用对外 handler)。nil 时对应端点返 503。
	Messages       *api.MessagesHandler
	Transcriptions *api.TranscriptionsHandler

	// Cache — 默认 chat 模型查询 (Phase B): brain ChatRunner 经
	// /v1/internal/models/default-chat 拉 admin 指定的默认模型。
	// nil 时该端点返 503。
	Cache *registry.Cache
}

// Mount registers the credentials internal route. Wired in startAdminStack
// where the Vault is available.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc(
		"POST /v1/internal/credentials/{id}/get-decrypted",
		s.requireToken(s.handleGetDecrypted),
	)
}

// MountGenerations registers the generation egress route. Wired separately
// in run() because it depends on *api.ImagesHandler / *api.VideosHandler,
// which are constructed after startAdminStack returns.
// 段 3.6: aigc worker 经此把生成流量导回 model-relay(I6 单一 egress)。
func (s *Server) MountGenerations(mux *http.ServeMux) {
	mux.HandleFunc(
		"POST /v1/internal/generations",
		s.requireToken(s.handleGenerate),
	)
}

// MountChat registers the internal LLM route. Depends on Messages handler
// (constructed in run() — wire after it's available). 爆款解析 worker 用。
func (s *Server) MountChat(mux *http.ServeMux) {
	mux.HandleFunc(
		"POST /v1/internal/chat",
		s.requireToken(s.handleChat),
	)
}

// MountTranscribe registers the internal STT route. Depends on Transcriptions
// handler (constructed in run() — wire after it's available). 爆款解析 worker 用。
func (s *Server) MountTranscribe(mux *http.ServeMux) {
	mux.HandleFunc(
		"POST /v1/internal/transcribe",
		s.requireToken(s.handleTranscribe),
	)
}

// MountModels registers the internal model-metadata routes. Depends on
// Cache (constructed in startAdminStack). brain ChatRunner 用 (Phase B)。
func (s *Server) MountModels(mux *http.ServeMux) {
	mux.HandleFunc(
		"GET /v1/internal/models/default-chat",
		s.requireToken(s.handleDefaultChatModel),
	)
}

// requireToken is the bearer-check middleware. Same pattern as
// services/identity/internal/internalapi/internalapi.go:57.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" {
			got := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(got) <= len(prefix) || got[:len(prefix)] != prefix {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			if subtle.ConstantTimeCompare(
				[]byte(got[len(prefix):]),
				[]byte(s.Token),
			) != 1 {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

// handleGetDecrypted decrypts a credentials row and returns the
// plaintext + base_url + header_override. Caller (aigc worker / orchestrator)
// uses these to dispatch to the upstream provider.
//
// Response shape kept flat so consumers don't need a typed schema.
// key_preview is included so callers can safely log "which key was used"
// without leaking the plaintext.
func (s *Server) handleGetDecrypted(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad credential id", http.StatusBadRequest)
		return
	}
	if s.Vault == nil {
		http.Error(w, "vault not wired", http.StatusServiceUnavailable)
		return
	}

	plaintext, baseURL, headers, err := s.Vault.Reveal(r.Context(), id)
	if err != nil {
		// Distinguish not-found / inactive from internal errors so
		// callers can react. Keep error text minimal — never echo
		// the underlying envelope error which may include credential
		// metadata.
		if errors.Is(err, registry.ErrNotFound) {
			http.Error(w, "credential not found", http.StatusNotFound)
			return
		}
		// Inactive credential or decrypt failure — both 503; admin
		// rotation path can fix and aigc will retry.
		http.Error(w, "credential unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store") // never cache plaintext
	_ = json.NewEncoder(w).Encode(map[string]any{
		"api_key":  string(plaintext),
		"base_url": baseURL,
		"headers":  headers,
	})

	// Best-effort wipe of the plaintext slice — Go strings are immutable
	// so the conversion above already copied; we still zero the byte
	// slice we hold to shrink the window where it's in heap.
	for i := range plaintext {
		plaintext[i] = 0
	}
}
