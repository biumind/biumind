// Package adminapi exposes the /v1/admin/* HTTP surface for the
// model_relay configuration backend. Handlers are stateless except
// for the shared Server struct (registry / cache / vault / probe /
// strategies / role cache).
//
// Conventions:
//   * All success responses are JSON with the resource at the root.
//     List endpoints wrap in {"items": [...], "total": N}.
//   * All errors are JSON {"error": {"code":"...", "message":"...", "field?":"..."}}.
//     `code` is a stable token admin frontends can branch on; `message`
//     is human-readable. `field` is optional, set when the error is
//     about a specific input field.
//   * Permission gates are applied at the route table level (Server.Mount)
//     via RoleCache.RequirePermission — handlers MAY assume claims are
//     already in ctx via bauth.WithClaims.

package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// ErrorBody is the wire shape for every error response. Stable so
// admin frontends can branch on `code` without parsing `message`.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// writeError formats and writes an error response. status is the HTTP
// code; code is the stable string token. Logs at the warn level — the
// actual log decision is left to the caller (handlers may want to log
// at debug for expected 4xx).
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorField(w, status, code, message, "")
}

func writeErrorField(w http.ResponseWriter, status int, code, message, field string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: ErrorBody{Code: code, Message: message, Field: field},
	})
}

// writeJSON marshals body and sends it with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// translateRegistryError maps repo sentinels to HTTP responses.
// Handlers use this in the "I got an error from a Repo / Cache call"
// tail; bespoke validation errors (bad input shape) should be written
// directly via writeErrorField with status 400.
func translateRegistryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, registry.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// decodeJSON reads + unmarshals the request body. Returns an HTTP
// status code; handler returns immediately on non-zero. Limits body
// to 256KB — admin payloads are tiny, larger inputs are pathological.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return false
	}
	return true
}
