// Package api implements Authz HTTP endpoints:
//
//	POST /v1/authz/check          single decision
//	POST /v1/authz/batch_check    multiple decisions for one principal
//	POST /v1/authz/reload         reload policies from disk (admin)
//	GET  /v1/authz/policies       count / metadata
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/biumind/biumind/services/authz/internal/cache"
	"github.com/biumind/biumind/services/authz/internal/engine"
	"github.com/biumind/biumind/services/authz/internal/policies"
)

type Server struct {
	Engine    *engine.Engine
	Cache     *cache.DecisionCache
	PolicyDir string
	Logger    *slog.Logger
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/authz/check", s.handleCheck)
	mux.HandleFunc("POST /v1/authz/batch_check", s.handleBatchCheck)
	mux.HandleFunc("POST /v1/authz/reload", s.handleReload)
	mux.HandleFunc("GET /v1/authz/policies", s.handlePolicyMeta)
}

// ─── Schemas (mirror proto/biumind/authz/v1) ────────────

type principalIn struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"` // optional override; default "User"
	Attributes map[string]any `json:"attributes"`
	Parents    []entityRef    `json:"parents"`
}

type resourceIn struct {
	Service    string         `json:"service"` // "wiki" / "graph"
	Type       string         `json:"type"`    // "Page" / "Node"
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
	Parents    []entityRef    `json:"parents"`
}

type entityRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type checkRequest struct {
	Principal principalIn    `json:"principal"`
	Action    string         `json:"action"`
	Resource  resourceIn     `json:"resource"`
	Context   map[string]any `json:"context"`
}

type checkResponse struct {
	Decision        string   `json:"decision"` // "ALLOW" / "DENY"
	Reason          string   `json:"reason"`
	MatchedPolicies []string `json:"matched_policies"`
	FromCache       bool     `json:"from_cache"`
	Errors          []string `json:"errors,omitempty"`
}

type batchItem struct {
	Action   string     `json:"action"`
	Resource resourceIn `json:"resource"`
}

type batchCheckRequest struct {
	Principal principalIn    `json:"principal"`
	Items     []batchItem    `json:"items"`
	Context   map[string]any `json:"context"`
}

type batchCheckResponse struct {
	Decisions []checkResponse `json:"decisions"`
}

// ─── Handlers ───────────────────────────────────────────

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resp, err := s.evaluate(req.Principal, req.Action, req.Resource, req.Context)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "evaluate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBatchCheck(w http.ResponseWriter, r *http.Request) {
	var req batchCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	out := batchCheckResponse{Decisions: make([]checkResponse, 0, len(req.Items))}
	for _, it := range req.Items {
		resp, err := s.evaluate(req.Principal, it.Action, it.Resource, req.Context)
		if err != nil {
			resp = checkResponse{Decision: "DENY", Reason: "evaluator error", Errors: []string{err.Error()}}
		}
		out.Decisions = append(out.Decisions, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) evaluate(p principalIn, action string, res resourceIn, ctx map[string]any) (checkResponse, error) {
	if action == "" {
		return checkResponse{}, errors.New("missing action")
	}
	pType := p.Type
	if pType == "" {
		pType = "User"
	}
	rType := res.Type
	if rType == "" {
		rType = "Resource"
	}

	key := decisionKey(pType+"::"+p.ID, action, rType+"::"+res.ID, ctx)
	if hit, ok := s.Cache.Get(key); ok {
		return checkResponse{
			Decision:        decisionString(hit.Decision),
			Reason:          hit.Reason,
			MatchedPolicies: hit.MatchedPolicies,
			FromCache:       true,
		}, nil
	}

	in := engine.Input{
		Principal: engine.Entity{
			Type: pType, ID: p.ID, Attributes: p.Attributes,
			Parents: convertParents(p.Parents),
		},
		Action: action,
		Resource: engine.Entity{
			Type: rType, ID: res.ID, Attributes: res.Attributes,
			Parents: convertParents(res.Parents),
		},
		Context: ctx,
	}
	r, err := s.Engine.Check(in)
	if err != nil {
		return checkResponse{}, err
	}
	cd := cache.DecisionUnspecified
	switch r.Decision {
	case engine.DecisionAllow:
		cd = cache.DecisionAllow
	case engine.DecisionDeny:
		cd = cache.DecisionDeny
	}
	s.Cache.Set(key, cache.Entry{
		Decision:        cd,
		Reason:          r.Reason,
		MatchedPolicies: r.MatchedPolicies,
	})
	if s.Logger != nil {
		s.Logger.Debug("authz: check",
			"principal_type", pType, "principal_id", p.ID,
			"action", action, "resource_type", rType, "resource_id", res.ID,
			"decision", r.Decision.String(), "matched_policies", len(r.MatchedPolicies))
	}
	return checkResponse{
		Decision:        r.Decision.String(),
		Reason:          r.Reason,
		MatchedPolicies: r.MatchedPolicies,
		FromCache:       false,
		Errors:          r.Errors,
	}, nil
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.PolicyDir == "" {
		writeErr(w, http.StatusFailedDependency, "no_policy_dir", "POLICIES_PATH not configured")
		return
	}
	raw, files, err := policies.LoadDir(s.PolicyDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if err := s.Engine.LoadPolicies(raw); err != nil {
		writeErr(w, http.StatusInternalServerError, "parse_failed", err.Error())
		return
	}
	s.Cache.Clear()
	s.Logger.Info("policies reloaded", "files", files, "count", s.Engine.PolicyCount())
	writeJSON(w, http.StatusOK, map[string]any{
		"loaded_files": files,
		"policy_count": s.Engine.PolicyCount(),
	})
}

func (s *Server) handlePolicyMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"policy_count": s.Engine.PolicyCount(),
		"cache_size":   s.Cache.Len(),
	})
}

// ─── helpers ────────────────────────────────────────────

func decisionKey(principal, action, resource string, ctx map[string]any) string {
	h := sha256.New()
	h.Write([]byte(principal))
	h.Write([]byte("|"))
	h.Write([]byte(action))
	h.Write([]byte("|"))
	h.Write([]byte(resource))
	h.Write([]byte("|"))
	if len(ctx) > 0 {
		// sort keys to make hash stable
		keys := make([]string, 0, len(ctx))
		for k := range ctx {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			h.Write([]byte(k))
			h.Write([]byte("="))
			fmt.Fprintf(h, "%v", ctx[k])
			h.Write([]byte(";"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func convertParents(in []entityRef) []engine.EntityRef {
	out := make([]engine.EntityRef, 0, len(in))
	for _, p := range in {
		out = append(out, engine.EntityRef{Type: p.Type, ID: p.ID})
	}
	return out
}

func decisionString(d cache.Decision) string {
	switch d {
	case cache.DecisionAllow:
		return "ALLOW"
	case cache.DecisionDeny:
		return "DENY"
	}
	return "UNSPECIFIED"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": strings.TrimSpace(msg)},
	})
}
