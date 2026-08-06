// Package internalapi exposes service-to-service endpoints that are
// not safe to put on the public Identity API but need to be reachable
// by sibling services (model-relay, Brain, …) inside the cluster.
//
// Authentication: a single shared bearer token (HUB_INTERNAL_TOKEN).
// In production the token comes from ESO; in dev/CI it's set in the
// kustomize ConfigMap. NetworkPolicy restricts the path to in-cluster
// pods, so the token is a defence-in-depth measure rather than a
// primary auth mechanism.
//
// Currently exposes:
//
//	GET /v1/internal/users/{id}/plan → {"plan":"pro"}
//
// The model-relay plan resolver calls this once per cache miss (60 s TTL) so
// load is bounded.

package internalapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/identity/internal/byok"
	"github.com/biumind/biumind/services/identity/internal/credits"
)

// PlanLookup resolves a user id to their billing plan name. Returns
// "" + nil error when the user has no recorded plan (treated as free
// by callers).
type PlanLookup func(userID string) (plan string, err error)

// Server bundles the internal handlers.
type Server struct {
	// Token is the shared bearer expected on every request. Empty
	// disables auth entirely — only acceptable in tests.
	Token string
	// Lookup resolves user → plan. Required.
	Lookup PlanLookup

	// Credits 由 MountCredits 注入. nil 时 credits.* endpoint 不挂.
	Credits *credits.Service
	// BYOK 由 MountBYOK 注入. nil 时 byok.* endpoint 不挂.
	BYOK *byok.Store
}

func New(token string, lookup PlanLookup) *Server {
	return &Server{Token: token, Lookup: lookup}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/internal/users/{id}/plan", s.requireToken(s.handlePlan))
}

func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" {
			got := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(got) <= len(prefix) || got[:len(prefix)] != prefix {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			// Constant-time compare to defeat timing attacks on the
			// shared secret. Length-mismatch path returns false.
			if subtle.ConstantTimeCompare([]byte(got[len(prefix):]),
				[]byte(s.Token)) != 1 {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing user id", http.StatusBadRequest)
		return
	}
	if s.Lookup == nil {
		http.Error(w, "lookup not wired", http.StatusInternalServerError)
		return
	}
	plan, err := s.Lookup(id)
	if err != nil {
		// Distinguish not-found from other errors so callers can
		// fall back to free without alerting.
		if errors.Is(err, ErrNotFound) {
			plan = ""
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if plan == "" {
		plan = "free"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"plan": plan})
}

// ErrNotFound — wrap with errors.Is when a lookup returns no row, so
// the handler can degrade gracefully to "free" instead of 500.
var ErrNotFound = errors.New("user not found")
