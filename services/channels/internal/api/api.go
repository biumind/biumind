// Package api implements the Channels HTTP surface.
//
//	POST   /v1/channels/{channel}/webhook    inbound (no JWT — driver verifies platform sig)
//	POST   /v1/channels/send                 outbound (JWT — picks driver by Envelope.Channel)
//	GET    /v1/channels/recent?n=N           debug: replay router's ring buffer
//	GET    /v1/channels                      list registered drivers
//
// The webhook route is intentionally unauthenticated at the JWT layer
// because each platform has its own signing scheme (Telegram secret
// header, Slack HMAC, Discord Ed25519, …). Drivers verify in
// VerifyAndParse(); the API layer maps signature errors to 401.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/channels/internal/driver"
	"github.com/biumind/biumind/services/channels/internal/envelope"
	"github.com/biumind/biumind/services/channels/internal/router"
)

type Server struct {
	Router   *router.Router
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(rt *router.Router, v *bauth.Verifier, l *slog.Logger) *Server {
	if l == nil {
		l = slog.Default()
	}
	return &Server{Router: rt, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/channels/{channel}/webhook", s.handleWebhook)
	mux.HandleFunc("POST /v1/channels/send", s.requireAuth(s.handleSend))
	mux.HandleFunc("GET /v1/channels", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/channels/recent", s.requireAuth(s.handleRecent))
}

// ─── Webhook (unauthenticated, driver-verified) ───────────

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")
	d, ok := s.Router.Driver(channel)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown_channel", channel)
		return
	}
	envs, err := d.VerifyAndParse(r)
	if err != nil {
		switch {
		case errors.Is(err, driver.ErrUnsigned):
			writeErr(w, http.StatusUnauthorized, "missing_signature", "")
		case errors.Is(err, driver.ErrSignatureInvalid):
			writeErr(w, http.StatusUnauthorized, "bad_signature", "")
		default:
			writeErr(w, http.StatusBadRequest, "parse_failed", err.Error())
		}
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "channels: webhook",
			"channel", channel, "envelopes", len(envs),
			"remote", r.RemoteAddr)
	}
	if len(envs) > 0 {
		s.Router.Inbound(r.Context(), envs)
	}
	// Platforms expect a fast 200 — the router runs forwarding async
	// best-effort.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "received": len(envs)})
}

// ─── Outbound send ────────────────────────────────────────

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	var env envelope.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if env.Channel == "" {
		writeErr(w, http.StatusBadRequest, "missing_channel", "")
		return
	}
	d, ok := s.Router.Driver(env.Channel)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown_channel", env.Channel)
		return
	}
	out, err := d.Send(r.Context(), env)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "send_failed", err.Error())
		return
	}
	if s.Logger != nil {
		s.Logger.DebugContext(r.Context(), "channels: send",
			"channel", env.Channel, "conversation_id", env.ConversationID,
			"text_bytes", len(env.Text), "attachments", len(env.Attachments))
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── List + recent ────────────────────────────────────────

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"channels": s.Router.Routes()})
}

func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 50
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"envelopes": s.Router.Recent(n),
	})
}

// ─── helpers ──────────────────────────────────────────────

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(auth[7:])
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
