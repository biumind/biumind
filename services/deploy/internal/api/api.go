// Package api implements the Deploy HTTP surface.
//
//	POST   /v1/deploys                multipart: kind=static|container, tarball=…, port=…(container)
//	GET    /v1/deploys                list (owner-scoped)
//	GET    /v1/deploys/{id}           get
//	DELETE /v1/deploys/{id}           destroy
//	GET    /v1/deploys/{id}/logs      stream logs (text/plain, flushed)
//
// Multipart parts:
//
//	kind        — required, "static" or "container"
//	label       — optional human label
//	tarball     — required, application/gzip; tar inside is the deploy content
//	port        — container only, integer; the port app listens on inside
//	env_<KEY>=v — container only, repeatable
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/deploy/internal/driver"
)

// 1 GB ceiling on the multipart body so we don't OOM the service.
const maxUploadBytes = 1 << 30

type Server struct {
	Driver   driver.Driver
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(d driver.Driver, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Driver: d, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/deploys", s.requireAuth(s.handleDeploy))
	mux.HandleFunc("GET /v1/deploys", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/deploys/{id}", s.requireAuth(s.handleGet))
	mux.HandleFunc("DELETE /v1/deploys/{id}", s.requireAuth(s.handleDestroy))
	mux.HandleFunc("GET /v1/deploys/{id}/logs", s.requireAuth(s.handleLogs))
}

// ─── Handlers ─────────────────────────────────────────────

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_multipart", err.Error())
		return
	}
	plan := driver.Plan{
		OwnerID: ownerID(r),
		Env:     map[string]string{},
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_part", err.Error())
			return
		}
		switch part.FormName() {
		case "kind":
			b, _ := io.ReadAll(part)
			plan.Kind = strings.TrimSpace(string(b))
		case "label":
			b, _ := io.ReadAll(part)
			plan.Label = string(b)
		case "port":
			b, _ := io.ReadAll(part)
			n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			plan.ContainerPort = n
		case "tarball":
			// Pass the part stream straight to the driver — don't
			// buffer the whole thing.
			plan.Tarball = part
			dep, derr := s.Driver.Deploy(r.Context(), plan)
			if derr != nil {
				writeErr(w, http.StatusBadRequest, "deploy_failed", derr.Error())
				return
			}
			writeJSON(w, http.StatusOK, deploymentOut(dep))
			return
		default:
			if name, ok := strings.CutPrefix(part.FormName(), "env_"); ok {
				b, _ := io.ReadAll(part)
				plan.Env[name] = string(b)
			} else {
				_, _ = io.Copy(io.Discard, part)
			}
		}
	}
	writeErr(w, http.StatusBadRequest, "missing_tarball",
		"tarball part must come last so driver can stream it")
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	deps, err := s.Driver.List(r.Context(), ownerID(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(deps))
	for _, d := range deps {
		out = append(out, deploymentOut(&d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": out})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	d, err := s.requireOwned(r)
	if err != nil {
		writeDriverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deploymentOut(d))
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

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwned(r); err != nil {
		writeDriverErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	pipe := &flushingWriter{w: w, flusher: flusher}
	if err := s.Driver.Logs(r.Context(), r.PathValue("id"), pipe); err != nil {
		_, _ = io.WriteString(pipe, "\n[logs error: "+err.Error()+"]\n")
	}
}

// ─── helpers ──────────────────────────────────────────────

func (s *Server) requireOwned(r *http.Request) (*driver.Deployment, error) {
	id := r.PathValue("id")
	d, err := s.Driver.Get(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if d.OwnerID != ownerID(r) {
		return nil, errForbidden
	}
	return d, nil
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
			s.Logger.DebugContext(r.Context(), "deploy api: request",
				"user_id", claims.UserID, "method", r.Method, "path", r.URL.Path)
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func ownerID(r *http.Request) string {
	return bauth.MustClaims(r.Context()).UserID
}

func deploymentOut(d *driver.Deployment) map[string]any {
	out := map[string]any{
		"id":         d.ID,
		"owner_id":   d.OwnerID,
		"kind":       d.Kind,
		"status":     d.Status,
		"created_at": d.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": d.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if d.URL != "" {
		out["url"] = d.URL
	}
	if d.Image != "" {
		out["image"] = d.Image
	}
	if d.Error != "" {
		out["error"] = d.Error
	}
	return out
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
	case errors.Is(err, errForbidden):
		writeErr(w, http.StatusForbidden, "forbidden", "")
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

type flushingWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (f *flushingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if f.flusher != nil {
		f.flusher.Flush()
	}
	return n, err
}
