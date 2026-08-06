// internal.go — service-to-service routes gated by a shared secret
// (MODEL_RELAY_INTERNAL_TOKEN) instead of a user JWT.
//
// Used by background platform workers that have no user request context
// — e.g. brain's embedder indexing wiki/memory/graph vectors. The
// handler behind such a route resolves the platform pool: no per-user
// BYOK lookup, no per-user billing. This is the embedding-as-infra
// lane, symmetric to identity's IDENTITY_INTERNAL_TOKEN pattern.
package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// InternalTokenMiddleware gates a route with a shared secret. Accepts
// the token in X-Biumind-Internal-Token (canonical) OR
// Authorization: Bearer <token> — the Bearer form lets OpenAI-shaped
// clients (brain's embedder) call without a custom header. An empty
// configured token rejects every request, effectively disabling the
// route (workers must then configure the env or stay off the path).
func InternalTokenMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Biumind-Internal-Token")
		if got == "" {
			if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
				got = strings.TrimPrefix(a, "Bearer ")
			}
		}
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeJSONErr(w, http.StatusUnauthorized, "invalid_internal_token", "")
			return
		}
		next.ServeHTTP(w, r)
	})
}
