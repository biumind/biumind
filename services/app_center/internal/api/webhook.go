// Webhook receiver.
//
//	POST /webhooks/app_center/{install_id}/{path...}
//
// Inbound flow:
//
//   1. Look up the installation row (404 if missing) and its
//      webhook_secret (404 if NULL — the install declared no webhooks).
//   2. Resolve the matching scheduler_jobs row by (install_id,
//      webhook_path). path is the URL tail after install_id.
//   3. HMAC-SHA256 verify the body against webhook_secret using
//      X-BiuMind-App-Signature header. Constant-time compare.
//   4. Build a TriggerEvent and dispatch via biuapp.Registry.
//   5. Record an invocations row (caller=webhook).
//   6. Return 200 with {"ok": true} on success; 401 on bad sig;
//      404 on unknown install / unmounted webhook; 500 on app failure.
//
// Auth model: NO JWT here. Webhook callers are external services
// (Stripe, Gmail push, etc.) that authenticate via the HMAC. We do
// require a non-empty body (some providers send GET pings — those
// are rejected; configure the upstream to use POST + a payload).

package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/triggers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MountWebhooks attaches the public webhook receiver. We mount it on
// the same mux as /v1/apps/* but the path doesn't go through
// requireAuth — the HMAC IS the auth.
//
// Returns the same Server for chaining alongside other Mount calls.
func (s *Server) MountWebhooks(mux *http.ServeMux) {
	if s.Pool == nil || s.Registry == nil {
		// No DB / no registry — webhook path returns 503 cleanly.
		return
	}
	mux.HandleFunc("POST /webhooks/app_center/{install_id}/{path...}", s.handleWebhook)
}

// Pool, Registry, Logger fields are wired by the runtime daemon into
// Server (M4.6) so the webhook handler can reach them. We add them as
// pointers on Server so existing tests don't have to construct them.
//
// (These are intentionally NOT in api.go because that file is the
// v1.0 surface — keeping webhook plumbing here makes M4 a single
// contained slice.)

// SetPool attaches the pgx pool used by the webhook handler. main.go
// calls this after constructing apiSrv.
func (s *Server) SetPool(p *pgxpool.Pool) { s.Pool = p }

// SetBiuappRegistry attaches the biuapp.Registry the webhook handler
// dispatches into. main.go calls this after constructing apiSrv.
func (s *Server) SetBiuappRegistry(r *biuapp.Registry) { s.Registry = r }

// handleWebhook is the public-facing receiver.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	installID, err := uuid.Parse(r.PathValue("install_id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "invalid_install_id", "")
		return
	}
	// path... captures "everything after install_id", with no leading
	// slash. We re-add it because manifest.path stored in the DB does
	// have one ("/callback").
	pathTail := "/" + r.PathValue("path")

	// Body must be available before HMAC. Read it once; downstream
	// dispatch needs it too.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	if len(body) == 0 {
		writeErr(w, http.StatusBadRequest, "empty_body", "webhooks must POST a non-empty payload")
		return
	}

	// 1. Look up the installation + secret.
	var (
		secret     []byte
		identifier string
		enabled    bool
	)
	err = s.Pool.QueryRow(r.Context(), `
		SELECT webhook_secret, identifier, enabled
		  FROM app_center.installations
		 WHERE id = $1
	`, installID).Scan(&secret, &identifier, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "unknown_install", "")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db", err.Error())
		return
	}
	if !enabled {
		writeErr(w, http.StatusForbidden, "install_disabled", "")
		return
	}
	if len(secret) == 0 {
		// The install row exists but the App declared no webhooks.
		writeErr(w, http.StatusNotFound, "no_webhooks", "")
		return
	}

	// 2. Match the path against scheduler_jobs.
	var (
		jobID  uuid.UUID
		action string
	)
	err = s.Pool.QueryRow(r.Context(), `
		SELECT id, action FROM app_center.scheduler_jobs
		 WHERE install_id = $1 AND kind = 'webhook'
		   AND webhook_path = $2 AND enabled = true
	`, installID, pathTail).Scan(&jobID, &action)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "unmounted_path", pathTail)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db", err.Error())
		return
	}

	// 3. HMAC verify.
	sigHeader := r.Header.Get("X-BiuMind-App-Signature")
	if sigHeader == "" {
		// Some upstreams use a different header name (Stripe uses
		// Stripe-Signature, GitHub uses X-model-relay-Signature-256). For
		// v1.5 we standardise on ours; future versions could add a
		// per-app header alias. Reject cleanly.
		writeErr(w, http.StatusUnauthorized, "missing_signature", "expected X-BiuMind-App-Signature")
		return
	}
	if err := triggers.Verify(secret, body, sigHeader); err != nil {
		// Constant: same error shape regardless of cause (timing
		// side-channel hardening — see triggers/secret.go).
		writeErr(w, http.StatusUnauthorized, "invalid_signature", "")
		return
	}

	// 4. Dispatch.
	if s.Registry == nil {
		writeErr(w, http.StatusServiceUnavailable, "registry_unwired", "")
		return
	}
	ev := biuapp.TriggerEvent{
		TriggerKind: biuapp.TriggerWebhook,
		Action:      action,
		Input:       json.RawMessage(body),
		FiredAt:     time.Now().UTC(),
		Install: biuapp.Install{
			ID:         installID.String(),
			Identifier: identifier,
		},
	}

	start := time.Now()
	dispatchErr := s.Registry.DispatchOnTrigger(r.Context(), identifier, ev)
	durationMs := int(time.Since(start).Milliseconds())

	status := "ok"
	errMsg := ""
	if dispatchErr != nil {
		status = "error"
		errMsg = dispatchErr.Error()
	}

	// 5. Audit. Best-effort; a webhook delivery shouldn't fail just
	// because we couldn't write the audit row, but we log it.
	if _, err := s.Pool.Exec(r.Context(), `
		INSERT INTO app_center.invocations
			(install_id, app_id, identifier, action,
			 caller, caller_id, trace_id,
			 duration_ms, status, error_code)
		VALUES ($1, $2, $3, $4,
		        'webhook', $5, '',
		        $6, $7, $8)
	`, installID, "app_"+identifier, identifier, action,
		strings.TrimPrefix(pathTail, "/"), durationMs, status, truncate(errMsg, 80)); err != nil {
		if l := s.Logger; l != nil {
			l.Warn("webhook: audit insert", "err", err, "install_id", installID.String())
		}
	}

	// 6. Response.
	if dispatchErr != nil {
		writeErr(w, http.StatusInternalServerError, "dispatch_failed", errMsg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// silence unused-import linting in case the dispatcher file doesn't
// land in the same compilation unit (slog is consumed indirectly via
// Server.Logger).
var _ = slog.Default()
