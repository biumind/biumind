// Package api implements the Sandbox HTTP surface.
//
//	POST   /v1/sandboxes                    create
//	GET    /v1/sandboxes                    list (owner-scoped)
//	GET    /v1/sandboxes/{id}               get
//	DELETE /v1/sandboxes/{id}               destroy
//	POST   /v1/sandboxes/{id}/exec          stream stdout/stderr (SSE)
//	POST   /v1/sandboxes/{id}/pause
//	POST   /v1/sandboxes/{id}/resume
//	POST   /v1/sandboxes/{id}/snapshot
//
// All routes require a JWT bearer token. Owner scoping: callers can only
// see/manage sandboxes whose `owner` label matches their `sub` claim.
//
// SSE stream format (matches AG-UI Custom event convention):
//
//	event: stdout
//	data: {"chunk":"…"}
//
//	event: exit
//	data: {"code":0,"timed_out":false}

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	bmetrics "github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/biumind/biumind/packages/go-sdk/biu/quota"
	"github.com/biumind/biumind/services/sandbox/internal/driver"
)

type Server struct {
	Driver   driver.Driver
	Verifier *bauth.Verifier
	Logger   *slog.Logger

	// Quota — optional. nil disables both gates entirely.
	Quota quota.Limiter

	// MaxConcurrentPerOwner — soft cap on the number of running
	// sandboxes per JWT subject. 0 disables the check.
	MaxConcurrentPerOwner int
}

func NewServer(d driver.Driver, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Driver: d, Verifier: v, Logger: l}
}

// WithQuota wires the per-owner gates. Caller passes a Limiter
// configured with `sandbox.daily` bucket spec (see main.go).
func (s *Server) WithQuota(l quota.Limiter, maxConcurrent int) *Server {
	s.Quota = l
	s.MaxConcurrentPerOwner = maxConcurrent
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/sandboxes", s.requireAuth(s.handleCreate))
	mux.HandleFunc("GET /v1/sandboxes", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/sandboxes/{id}", s.requireAuth(s.handleGet))
	mux.HandleFunc("DELETE /v1/sandboxes/{id}", s.requireAuth(s.handleDestroy))
	mux.HandleFunc("POST /v1/sandboxes/{id}/exec", s.requireAuth(s.handleExec))
	mux.HandleFunc("POST /v1/sandboxes/{id}/pause", s.requireAuth(s.handlePause))
	mux.HandleFunc("POST /v1/sandboxes/{id}/resume", s.requireAuth(s.handleResume))
	mux.HandleFunc("POST /v1/sandboxes/{id}/snapshot", s.requireAuth(s.handleSnapshot))
}

// ─── Handlers ─────────────────────────────────────────────

type createReq struct {
	Image       string            `json:"image"`
	CPUShares   int               `json:"cpu_shares"`
	MemoryMB    int               `json:"memory_mb"`
	NetworkOff  *bool             `json:"network_off"`
	EgressAllow []string          `json:"egress_allow"`
	Env         map[string]string `json:"env"`
	Label       string            `json:"label"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	uid := ownerID(r)

	// Concurrent cap — count running sandboxes for this owner before
	// reserving any resources. List is owner-scoped already.
	if s.MaxConcurrentPerOwner > 0 {
		mine, err := s.Driver.List(r.Context(), uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		running := 0
		for _, sb := range mine {
			if sb.Status == "running" || sb.Status == "paused" || sb.Status == "creating" {
				running++
			}
		}
		if running >= s.MaxConcurrentPerOwner {
			bmetrics.RecordQuota("sandbox.concurrent", false,
				int64(s.MaxConcurrentPerOwner-running))
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "concurrent_limit",
				fmt.Sprintf("owner has %d active sandboxes (max %d); destroy one first",
					running, s.MaxConcurrentPerOwner))
			return
		}
		bmetrics.RecordQuota("sandbox.concurrent", true,
			int64(s.MaxConcurrentPerOwner-running-1))
	}

	// Daily-create gate via the shared quota Limiter.
	if s.Quota != nil {
		d := s.Quota.CheckAndReserve("sandbox.daily", uid, 1)
		for k, v := range d.Headers() {
			w.Header().Set(k, v)
		}
		bmetrics.RecordQuota("sandbox.daily", d.Allow, d.Remaining)
		if !d.Allow {
			w.Header().Set("Retry-After",
				fmt.Sprintf("%d", int(time.Until(d.Reset).Seconds())+1))
			writeErr(w, http.StatusTooManyRequests, "daily_limit",
				"sandbox daily quota exhausted")
			return
		}
	}

	netOff := true
	if req.NetworkOff != nil {
		netOff = *req.NetworkOff
	}
	sb, err := s.Driver.Create(r.Context(), driver.CreateInput{
		OwnerID:     uid,
		Image:       req.Image,
		CPUShares:   req.CPUShares,
		MemoryMB:    req.MemoryMB,
		NetworkOff:  netOff,
		EgressAllow: req.EgressAllow,
		Env:         req.Env,
		Label:       req.Label,
	})
	if err != nil {
		// Refund the daily quota when the driver itself failed to
		// produce a sandbox — caller didn't actually consume the slot.
		if s.Quota != nil {
			s.Quota.Refund("sandbox.daily", uid, 1)
		}
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sandboxOut(sb))
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	sbs, err := s.Driver.List(r.Context(), ownerID(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(sbs))
	for _, sb := range sbs {
		out = append(out, sandboxOut(&sb))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sandboxes": out})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	sb, err := s.requireOwned(r)
	if err != nil {
		writeDriverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sandboxOut(sb))
}

func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwned(r); err != nil {
		writeDriverErr(w, err)
		return
	}
	if err := s.Driver.Destroy(r.Context(), r.PathValue("id")); err != nil {
		writeDriverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type execReq struct {
	Argv       []string `json:"argv"`
	Workdir    string   `json:"workdir"`
	StdinB64   string   `json:"stdin"`
	TimeoutSec int      `json:"timeout_sec"`
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwned(r); err != nil {
		writeDriverErr(w, err)
		return
	}
	var req execReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(req.Argv) == 0 {
		writeErr(w, http.StatusBadRequest, "missing_argv", "")
		return
	}
	// R5: workdir 路径越界防护（空 OK = driver 默认；否则须在 /workspace 或
	// /tmp 下、无 ".."）。容器隔离是真边界,这是 API 边缘的纵深防御。
	if err := driver.AssertSandboxPath(req.Workdir); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_workdir", err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "no_flusher", "client transport does not support streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Heartbeat goroutine — keeps NAT/proxies from killing the SSE stream
	// when commands take >30s. Pure SSE comment lines are ignored by
	// EventSource clients.
	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	doneHb := make(chan struct{})
	go func() {
		for {
			select {
			case <-doneHb:
				return
			case <-hb.C:
				_, _ = io.WriteString(w, ": heartbeat\n\n")
				flusher.Flush()
			}
		}
	}()
	defer close(doneHb)

	pipe := &sseChunkWriter{w: w, flusher: flusher}
	res, err := s.Driver.Exec(r.Context(), driver.ExecInput{
		SandboxID:  r.PathValue("id"),
		Argv:       req.Argv,
		Workdir:    req.Workdir,
		Stdin:      []byte(req.StdinB64),
		TimeoutSec: req.TimeoutSec,
	}, pipe)
	if err != nil {
		writeSSE(w, flusher, "error", map[string]any{"message": err.Error()})
		return
	}
	writeSSE(w, flusher, "exit", map[string]any{
		"code":      res.ExitCode,
		"timed_out": res.TimedOut,
	})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwned(r); err != nil {
		writeDriverErr(w, err)
		return
	}
	if err := s.Driver.Pause(r.Context(), r.PathValue("id")); err != nil {
		writeDriverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwned(r); err != nil {
		writeDriverErr(w, err)
		return
	}
	if err := s.Driver.Resume(r.Context(), r.PathValue("id")); err != nil {
		writeDriverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwned(r); err != nil {
		writeDriverErr(w, err)
		return
	}
	snapID, err := s.Driver.Snapshot(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDriverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot_id": snapID})
}

// ─── helpers ──────────────────────────────────────────────

func (s *Server) requireOwned(r *http.Request) (*driver.Sandbox, error) {
	id := r.PathValue("id")
	sb, err := s.Driver.Get(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if sb.OwnerID != ownerID(r) {
		return nil, errForbidden
	}
	return sb, nil
}

var errForbidden = errors.New("forbidden")

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
			s.Logger.DebugContext(r.Context(), "sandbox api: request",
				"user_id", claims.UserID, "method", r.Method, "path", r.URL.Path)
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func ownerID(r *http.Request) string {
	return bauth.MustClaims(r.Context()).UserID
}

func sandboxOut(sb *driver.Sandbox) map[string]any {
	return map[string]any{
		"id":          sb.ID,
		"owner_id":    sb.OwnerID,
		"image":       sb.Image,
		"status":      sb.Status,
		"created_at":  sb.CreatedAt.UTC().Format(time.RFC3339),
		"cpu_shares":  sb.CPUShares,
		"memory_mb":   sb.MemoryMB,
		"network_off": sb.NetworkOff,
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

func writeDriverErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, driver.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, driver.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, driver.ErrNotSupported):
		writeErr(w, http.StatusNotImplemented, "not_supported", err.Error())
	case errors.Is(err, errForbidden):
		writeErr(w, http.StatusForbidden, "forbidden", "")
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// ─── SSE helpers ──────────────────────────────────────────

func writeSSE(w http.ResponseWriter, f http.Flusher, event string, payload any) {
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	f.Flush()
}

// sseChunkWriter pipes Driver.Exec stdout into SSE `stdout` events.
// One write call → one event so the consumer can reconstruct chunks.
type sseChunkWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseChunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	writeSSE(s.w, s.flusher, "stdout", map[string]any{"chunk": string(p)})
	return len(p), nil
}
