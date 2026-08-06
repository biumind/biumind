// Package healthz provides standard liveness / readiness / version endpoints.
// All BiuMind services mount these via Mount(mux, ...).
package healthz

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
)

// Probe checks one dependency. Return nil if healthy.
type Probe func(ctx context.Context) error

type Server struct {
	Service       string
	Version       string
	SchemaVersion int

	probes map[string]Probe
	mu     sync.RWMutex
	ready  atomic.Bool
}

func New(service, version string, schemaVersion int) *Server {
	return &Server{
		Service:       service,
		Version:       version,
		SchemaVersion: schemaVersion,
		probes:        make(map[string]Probe),
	}
}

// AddProbe registers a readiness probe.
func (s *Server) AddProbe(name string, p Probe) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes[name] = p
}

// SetReady marks the service ready (call after warm-up done).
func (s *Server) SetReady(b bool) { s.ready.Store(b) }

// Mount installs /healthz, /readyz, /api/version on the given mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleLive)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/api/version", s.handleVersion)
}

func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}
	s.mu.RLock()
	probes := make(map[string]Probe, len(s.probes))
	for k, v := range s.probes {
		probes[k] = v
	}
	s.mu.RUnlock()

	results := make(map[string]string, len(probes))
	allOk := true
	for name, p := range probes {
		if err := p(r.Context()); err != nil {
			results[name] = "fail: " + err.Error()
			allOk = false
		} else {
			results[name] = "ok"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if !allOk {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"service":        s.Service,
		"version":        s.Version,
		"schema_version": s.SchemaVersion,
	})
}
