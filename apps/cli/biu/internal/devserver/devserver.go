// Package devserver — local HTTP surface for `biu app run --dev`.
//
// One per CLI invocation. Hosts:
//
//	GET  /v1/dev/health        — used by the client to detect a live dev server
//	GET  /v1/dev/apps          — list of dev-loaded apps + their manifests
//	GET  /v1/dev/apps/{slug}/manifest
//	POST /v1/dev/apps/{slug}/invoke    — proxy to the subprocess, or mocks
//	GET  /v1/dev/events         — Server-Sent Events stream of state
//	                              transitions (manifest reloaded, subprocess
//	                              restarted, validation error)
//
// The contract is intentionally minimal — these endpoints are NOT
// part of the user-facing API surface, only consumed by the desktop
// client's "developer mode" panel during local development. They are
// not authenticated; the server binds to 127.0.0.1 only.
//
// State transitions feed an in-memory ring buffer of recent events so
// the SSE stream replays the last ~50 events on connection. This is
// what powers the "developer mode" panel's status timeline without
// needing a DB.

package devserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// DevApp is one app loaded by the dev server. Manifest is the parsed
// (and validated) shape; ManifestRaw is the raw YAML/JSON the client
// can show in dev tooling without re-serialising. SourcePath is the
// directory `biu app run --dev` was launched from — useful for the
// client to display "loaded from /Users/me/projects/foo".
type DevApp struct {
	Slug        string         `json:"slug"`
	Identifier  string         `json:"identifier"`
	Title       string         `json:"title"`
	Version     string         `json:"version"`
	Manifest    map[string]any `json:"manifest"`
	ManifestRaw string         `json:"manifest_raw,omitempty"`
	SourcePath  string         `json:"source_path"`
	Subprocess  *Subproc       `json:"subprocess,omitempty"`
	Mock        bool           `json:"mock,omitempty"`
}

// Subproc is the snapshot of a running app subprocess. Nil when the
// app is mock-only (--mock fixtures/) or the subprocess has crashed.
type Subproc struct {
	PID       int       `json:"pid"`
	Cmd       string    `json:"cmd"`
	StartedAt time.Time `json:"started_at"`
	State     string    `json:"state"` // "running" | "starting" | "exited" | "error"
}

// EventKind enumerates dev-server-internal event types pushed onto
// the SSE stream. Stays small + stable so the client can switch on it.
type EventKind string

const (
	EventManifestReloaded EventKind = "manifest_reloaded"
	EventValidationError  EventKind = "validation_error"
	EventSubprocStarted   EventKind = "subproc_started"
	EventSubprocExited    EventKind = "subproc_exited"
	EventSubprocLog       EventKind = "subproc_log"
	EventInvoke           EventKind = "invoke"
)

// Event is one record on the dev server SSE feed.
type Event struct {
	At      time.Time `json:"at"`
	Kind    EventKind `json:"kind"`
	Slug    string    `json:"slug,omitempty"`
	Message string    `json:"message,omitempty"`
	// Detail carries kind-specific payload as opaque JSON so the
	// client can pick fields it knows about and ignore the rest.
	Detail map[string]any `json:"detail,omitempty"`
}

// Invoker is what the HTTP layer dispatches into when /invoke arrives.
// Devserver doesn't know whether the call hits a subprocess, an
// in-process bound App, or a fixture file — that's the orchestrator's
// concern (cmd/biu/app_run_cmd.go).
type Invoker interface {
	Invoke(ctx context.Context, slug, action string, input json.RawMessage) (any, error)
}

// Server holds the runtime state. Construct via New, mount via
// http.Server.Handler, and feed transitions via PushEvent / SetApps.
type Server struct {
	mu      sync.RWMutex
	apps    map[string]DevApp
	events  []Event
	maxBuf  int
	invoker Invoker

	// SSE clients: write to each on PushEvent. Closed when the request
	// ends; we GC by reading from doneCh.
	clientsMu sync.Mutex
	clients   map[chan Event]struct{}

	// Diagnostic — bind addr after Start().
	addr string
}

// New constructs a server with default config. maxBuf=50 events.
func New(invoker Invoker) *Server {
	return &Server{
		apps:    map[string]DevApp{},
		events:  make([]Event, 0, 64),
		maxBuf:  50,
		clients: map[chan Event]struct{}{},
		invoker: invoker,
	}
}

// SetApps replaces the registered app set wholesale. Used after a
// manifest reload so removed/renamed apps disappear from the list.
func (s *Server) SetApps(list []DevApp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps = make(map[string]DevApp, len(list))
	for _, a := range list {
		s.apps[a.Slug] = a
	}
}

// UpsertApp adds or replaces a single app. Convenience for callers
// that prefer not to rebuild the whole list.
func (s *Server) UpsertApp(a DevApp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps[a.Slug] = a
}

// PushEvent records and broadcasts a state transition. Non-blocking:
// SSE clients with a full channel drop the event rather than block
// the producer (the events buffer still keeps it for replay).
func (s *Server) PushEvent(e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	s.mu.Lock()
	if len(s.events) >= s.maxBuf {
		s.events = s.events[1:]
	}
	s.events = append(s.events, e)
	s.mu.Unlock()

	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- e:
		default: // drop to avoid blocking under burst
		}
	}
}

// Addr returns the listener address (post-Start). Empty before Start.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Start binds the server to addr (e.g. "127.0.0.1:7099") and serves
// until ctx is canceled. Returns the actual listener so callers that
// want :0 to mean "any free port" can read it back.
func (s *Server) Start(ctx context.Context, addr string) (string, <-chan error, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("devserver: listen %s: %w", addr, err)
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/dev/health", s.handleHealth)
	mux.HandleFunc("GET /v1/dev/apps", s.handleListApps)
	mux.HandleFunc("GET /v1/dev/apps/{slug}/manifest", s.handleManifest)
	mux.HandleFunc("POST /v1/dev/apps/{slug}/invoke", s.handleInvoke)
	mux.HandleFunc("GET /v1/dev/events", s.handleEvents)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	return ln.Addr().String(), errCh, nil
}

// ─── Handlers ─────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"started_at": startedAt,
		"app_count":  len(s.apps),
	})
}

func (s *Server) handleListApps(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	out := make([]DevApp, 0, len(s.apps))
	for _, a := range s.apps {
		out = append(out, a)
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"apps": out})
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	s.mu.RLock()
	a, ok := s.apps[slug]
	s.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown_app", "slug": slug})
		return
	}
	writeJSON(w, http.StatusOK, a.Manifest)
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if s.invoker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "invoker_unwired"})
		return
	}
	var body struct {
		Action string          `json:"action"`
		Input  json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_json", "detail": err.Error()})
		return
	}
	if body.Action == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_action"})
		return
	}
	result, err := s.invoker.Invoke(r.Context(), slug, body.Action, body.Input)
	s.PushEvent(Event{
		Kind: EventInvoke, Slug: slug,
		Message: body.Action,
		Detail: map[string]any{
			"action":  body.Action,
			"success": err == nil,
		},
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invoke_failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan Event, 16)
	s.clientsMu.Lock()
	s.clients[ch] = struct{}{}
	s.clientsMu.Unlock()
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ch)
		s.clientsMu.Unlock()
	}()

	// Replay buffered events first so the panel hydrates immediately.
	s.mu.RLock()
	for _, e := range s.events {
		writeSSE(w, e)
	}
	s.mu.RUnlock()
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			writeSSE(w, e)
			flusher.Flush()
		}
	}
}

// ─── helpers ──────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeSSE(w http.ResponseWriter, e Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

var startedAt = time.Now().UTC()
