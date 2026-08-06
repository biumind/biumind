// Package api implements the HTTP handlers for Realtime.
//
//	GET  /v1/realtime/stream            single SSE per device, multi-topic
//	POST /v1/realtime/subscribe         add topics to existing stream
//	POST /v1/realtime/unsubscribe       remove topics
//	POST /v1/internal/publish           server-only publish path
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/realtime/internal/hub"
	"github.com/biumind/biumind/services/realtime/internal/ledger"
)

// AuthzClient is the contract this package needs from the Authz service.
// In tests we pass a stub; in production a real HTTP client.
type AuthzClient interface {
	CanSubscribe(ctx context.Context, principal, topic string) (bool, error)
}

type Server struct {
	Hub             *hub.Hub
	Ledger          *ledger.Ledger
	Authz           AuthzClient
	Verifier        *bauth.Verifier
	HeartbeatPeriod time.Duration
	Logger          *slog.Logger
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/realtime/stream", s.requireAuth(s.handleStream))
	mux.HandleFunc("POST /v1/realtime/subscribe", s.requireAuth(s.handleSubscribe))
	mux.HandleFunc("POST /v1/realtime/unsubscribe", s.requireAuth(s.handleUnsubscribe))
	mux.HandleFunc("POST /v1/internal/publish", s.handleInternalPublish) // mTLS in prod
}

// ─── Stream ─────────────────────────────────────────────

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	claims := bauth.MustClaims(r.Context())
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" {
		deviceID = randHex(8)
	}

	topics := splitTopics(r.URL.Query().Get("topics"))
	// Authz check per topic before subscribing
	for _, t := range topics {
		if s.Authz != nil {
			ok, err := s.Authz.CanSubscribe(r.Context(), claims.UserID, t)
			if err != nil {
				http.Error(w, "authz check failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if !ok {
				http.Error(w, "topic not allowed: "+t, http.StatusForbidden)
				return
			}
		}
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Register conn
	conn := s.Hub.Register(deviceID, claims.UserID, topics)
	defer conn.Close()

	// Replay missed events. If the client's Last-Event-ID is older than
	// the oldest event we still retain, we cannot guarantee delivery of
	// the gap — emit a system "desync" frame (code 4009) so the client
	// drops its cursor and triggers a full refetch (v2-6).
	if since := r.Header.Get("Last-Event-ID"); since != "" {
		if s.Ledger.IsBeyondRetention(topics, since) {
			writeFrame(w, flusher, ledger.Event{
				ID: ulidNow(), Kind: "desync", Topic: "system",
				Payload: jsonMust(map[string]any{
					"code":   4009,
					"reason": "last_event_id_beyond_retention",
					"since":  since,
				}),
			})
			// Skip replay (it'd be a partial / misleading subset). Client
			// is expected to clear its cursor + fetch state from REST.
		} else {
			for _, e := range s.Ledger.Replay(topics, since) {
				writeFrame(w, flusher, e)
			}
		}
	}

	// Open frame to confirm subscription
	writeFrame(w, flusher, ledger.Event{
		ID: ulidNow(), Kind: "open", Topic: "system",
		Payload: jsonMust(map[string]any{
			"device_id": deviceID,
			"topics":    topics,
		}),
	})

	heartbeat := time.NewTicker(s.HeartbeatPeriod)
	defer heartbeat.Stop()

	clientGone := r.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat %d\n\n", time.Now().Unix())
			flusher.Flush()
		case e, ok := <-conn.Out():
			if !ok {
				// model-relay closed us (slow consumer / reregister)
				writeFrame(w, flusher, ledger.Event{
					ID: ulidNow(), Kind: "close", Topic: "system",
					Payload: jsonMust(map[string]any{"reason": "hub_closed"}),
				})
				return
			}
			writeFrame(w, flusher, e)
		}
	}
}

func writeFrame(w io.Writer, f http.Flusher, e ledger.Event) {
	body := map[string]any{
		"topic":   e.Topic,
		"kind":    e.Kind,
		"payload": json.RawMessage(e.Payload),
	}
	if e.TraceID != "" {
		body["trace_id"] = e.TraceID
	}
	js, _ := json.Marshal(body)
	fmt.Fprintf(w, "id: %s\nevent: message\ndata: %s\n\n", e.ID, js)
	f.Flush()
}

// ─── Subscribe / Unsubscribe ────────────────────────────

type subReq struct {
	DeviceID string   `json:"device_id"`
	Topics   []string `json:"topics"`
}

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var req subReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	claims := bauth.MustClaims(r.Context())
	for _, t := range req.Topics {
		if s.Authz != nil {
			ok, err := s.Authz.CanSubscribe(r.Context(), claims.UserID, t)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "authz_error", err.Error())
				return
			}
			if !ok {
				writeErr(w, http.StatusForbidden, "topic_denied", t)
				return
			}
		}
		if err := s.Hub.Subscribe(req.DeviceID, t); err != nil {
			writeErr(w, http.StatusNotFound, "conn_not_found", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscribed": req.Topics})
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req subReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	for _, t := range req.Topics {
		_ = s.Hub.Unsubscribe(req.DeviceID, t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"unsubscribed": req.Topics})
}

// ─── Internal Publish (called by other Go services) ─────

type publishReq struct {
	Topic          string          `json:"topic"`
	Kind           string          `json:"kind"`
	Payload        json.RawMessage `json:"payload"`
	TraceID        string          `json:"trace_id"`
	IdempotencyKey string          `json:"idempotency_key"`
}

func (s *Server) handleInternalPublish(w http.ResponseWriter, r *http.Request) {
	// Production: gate by mTLS / shared secret; for MVP allow plain.
	var req publishReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Topic == "" || req.Kind == "" {
		writeErr(w, http.StatusBadRequest, "missing_field", "topic and kind required")
		return
	}
	e := ledger.Event{
		ID: ulidNow(), Topic: req.Topic, Kind: req.Kind,
		Payload: req.Payload, TraceID: req.TraceID, TS: time.Now(),
	}
	s.Ledger.Append(e)
	delivered, dropped := s.Hub.Publish(e)
	writeJSON(w, http.StatusOK, map[string]any{
		"event_id":  e.ID,
		"delivered": delivered,
		"dropped":   dropped,
	})
}

// ─── middleware / helpers ───────────────────────────────

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
		if s.Logger != nil {
			s.Logger.DebugContext(r.Context(), "realtime api: request",
				"user_id", claims.UserID, "method", r.Method,
				"path", r.URL.Path, "topics", r.URL.Query().Get("topics"))
		}
		r = r.WithContext(bauth.WithClaims(r.Context(), claims))
		next(w, r)
	}
}

func splitTopics(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ulidNow returns a sortable ULID-ish id (not strict ULID, but monotonic).
// Format: <ts ms in hex 12><random 8 hex>
func ulidNow() string {
	ms := time.Now().UnixMilli()
	r := make([]byte, 4)
	_, _ = rand.Read(r)
	return fmt.Sprintf("%012x%s", ms, hex.EncodeToString(r))
}

func jsonMust(v any) []byte {
	b, _ := json.Marshal(v)
	return b
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
