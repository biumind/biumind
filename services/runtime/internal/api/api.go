// Package api implements Runtime HTTP endpoints.
//
//	POST   /v1/agents/run                start a new task; returns task_id + run_id
//	GET    /v1/agents/tasks/{id}         status snapshot
//	GET    /v1/agents/tasks              list user's recent tasks
//	POST   /v1/agents/tasks/{id}/cancel  best-effort cancel
//
// Real-time output flows through Realtime topic agui:run:<run_id>.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/runtime/internal/agent"
	"github.com/biumind/biumind/services/runtime/internal/authz"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/biumind/biumind/services/runtime/internal/skills/installer"
	"github.com/biumind/biumind/services/runtime/internal/store"
	"github.com/google/uuid"
)

type Server struct {
	Agent     *agent.Agent
	Store     *store.Store
	Verifier  *bauth.Verifier
	Logger    *slog.Logger
	cancelMu  sync.Mutex
	cancelers map[uuid.UUID]context.CancelFunc

	// MemoryFor returns a per-request MemoryClient given the caller's
	// bearer token, so memory tools called from inside the agent loop
	// inherit the caller's identity (and can't read someone else's
	// memories). Optional — when nil, memory tools aren't registered.
	MemoryFor func(bearerToken string) agent.MemoryClient

	// Skills, when non-nil, mounts the /v1/skills/* surface (CRUD +
	// per-agent toggle + state-machine transitions). Optional — nil
	// keeps Skills routes unmounted, useful for daemon variants
	// that haven't migrated past the base agent loop yet.
	Skills *skillsreg.Registry

	// SkillsAuthz threads the Cedar policy gate into the agent's
	// per-run SkillToolDeps so high-risk Skills tools check Authz
	// before running. Optional — nil falls back to authz.AlwaysAllow
	// (dev / CLI mode); production daemon main.go wires
	// authz.NewHTTP(cfg.AuthzURL) explicitly.
	SkillsAuthz authz.Decider

	// SkillsSandbox is the runtime → services/sandbox client that
	// powers skill.exec_script. Optional — nil keeps the soft-error
	// path; production daemon main.go wires
	// rsandbox.New(cfg.SandboxURL, "") + per-request token forward.
	SkillsSandbox agent.SkillSandbox

	// SkillsWiki is the runtime → brain Wiki client used by
	// skill.read_wiki (P0-#3). Optional — nil makes the tool
	// soft-error with "wiki client not configured".
	SkillsWiki agent.WikiClient

	// SkillsTrustStore — ed25519 publisher pubkeys used to verify
	// .biuskill archives at install time (P2-#10). nil or empty
	// store = permissive mode (zip installs accepted unsigned);
	// non-empty store = strict mode (signature_b64 required).
	// Wired from runtime/main.go via installer.LoadTrustStoreFromEnv.
	SkillsTrustStore *installer.TrustStore

	// AppToolDepsFor returns the per-run agent.AppToolDeps for a
	// given (agentID, orgID) — wires the App Center installations
	// into the agent's tool fleet (M3.5). Optional — nil keeps app
	// tools unloaded; production daemon main.go wires
	// apptools.MakeAgentDeps. Function indirection avoids a circular
	// import (apptools → agent → api).
	AppToolDepsFor func(agentID uuid.UUID, orgID string) *agent.AppToolDeps
}

func NewServer(a *agent.Agent, s *store.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{
		Agent: a, Store: s, Verifier: v, Logger: l,
		cancelers: map[uuid.UUID]context.CancelFunc{},
	}
}

// WithMemory wires a Brain.Memory client factory. Returns the same
// server for chaining.
func (s *Server) WithMemory(factory func(token string) agent.MemoryClient) *Server {
	s.MemoryFor = factory
	return s
}

// WithAppTools wires a per-run AppToolDeps factory. The factory is
// called once per /v1/agents/run request with the resolved agentID
// + orgID; it should return the deps produced by apptools.MakeAgentDeps.
// nil keeps app tools unloaded — fine for daemon variants without DB.
func (s *Server) WithAppTools(f func(agentID uuid.UUID, orgID string) *agent.AppToolDeps) *Server {
	s.AppToolDepsFor = f
	return s
}

// WithSkills wires the Skills registry. Returns the same server for
// chaining. Nil registry keeps the /v1/skills routes unmounted —
// see Mount.
func (s *Server) WithSkills(r *skillsreg.Registry) *Server {
	s.Skills = r
	return s
}

// WithSkillsAuthz wires the Cedar policy gate into the per-run
// SkillToolDeps. Returns the same server for chaining.
func (s *Server) WithSkillsAuthz(d authz.Decider) *Server {
	s.SkillsAuthz = d
	return s
}

// WithSkillsSandbox wires the runtime → sandbox client into per-run
// SkillToolDeps so skill.exec_script can run commands. Returns the
// same server for chaining.
func (s *Server) WithSkillsSandbox(sb agent.SkillSandbox) *Server {
	s.SkillsSandbox = sb
	return s
}

// WithSkillsWiki wires the runtime → brain Wiki client into per-run
// SkillToolDeps so skill.read_wiki can search / read pages. Returns
// the same server for chaining.
func (s *Server) WithSkillsWiki(w agent.WikiClient) *Server {
	s.SkillsWiki = w
	return s
}

// WithSkillsTrustStore wires the ed25519 publisher trust store used
// by the install handler (P2-#10). nil / empty store = permissive
// mode; non-empty = strict mode (signature_b64 required on every
// zip install). Returns the same server for chaining.
func (s *Server) WithSkillsTrustStore(t *installer.TrustStore) *Server {
	s.SkillsTrustStore = t
	return s
}

func (s *Server) Mount(mux *http.ServeMux) {
	// /v1/agents/run 已删（S11-4）—— Agent Plane 路径替代
	mux.HandleFunc("GET /v1/agents/tasks/{id}", s.requireAuth(s.handleGetTask))
	mux.HandleFunc("GET /v1/agents/tasks", s.requireAuth(s.handleListTasks))
	mux.HandleFunc("POST /v1/agents/tasks/{id}/cancel", s.requireAuth(s.handleCancel))

	// Skills surface — only mount when a Registry is wired so daemon
	// variants opting out don't surface a misleading 500 path.
	if s.Skills != nil {
		mux.HandleFunc("GET /v1/skills", s.requireAuth(s.handleListSkills))
		mux.HandleFunc("GET /v1/skills/{id}", s.requireAuth(s.handleGetSkill))
		mux.HandleFunc("POST /v1/skills", s.requireAuth(s.handleInstallSkill))
		mux.HandleFunc("PATCH /v1/skills/{id}", s.requireAuth(s.handleUpdateSkill))
		mux.HandleFunc("DELETE /v1/skills/{id}", s.requireAuth(s.handleDeleteSkill))
		mux.HandleFunc("POST /v1/skills/{id}/toggle", s.requireAuth(s.handleToggleSkill))
		mux.HandleFunc("POST /v1/skills/propose", s.requireAuth(s.handleProposeSkill))
		mux.HandleFunc("POST /v1/skills/{id}/approve", s.requireAuth(s.handleApproveSkill))
		mux.HandleFunc("POST /v1/skills/{id}/reject", s.requireAuth(s.handleRejectSkill))
		mux.HandleFunc("POST /v1/skills/{id}/share-org", s.requireAuth(s.handleShareSkillOrg))
		mux.HandleFunc("GET /v1/skills/{id}/activations", s.requireAuth(s.handleListSkillActivations))
	}
}

// /v1/agents/run handler 删除（S11-4）—— 原来跑 agent.Run + AG-UI publish
// 到 agui:run:<runID> realtime topic，consumer 是 Flutter 老 client_agent_loop。
// 全栈现在走 brain Agent Plane（chat 模式 RunV2 / agent 模式 biu daemon /
// task 模式 runtime worker），channels 也切 brain。/v1/agents/run 没人调，
// 整段连同 agent.Run + publisher 一起删。
//
// 留下的 /v1/agents/tasks endpoints 仍能用 —— 它们读 store，不依赖 agent.Run。

// ─── Get / List ─────────────────────────────────────────

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	t, err := s.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	claims := bauth.MustClaims(r.Context())
	if t.UserID.String() != claims.UserID {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}
	writeJSON(w, http.StatusOK, taskOut(t))
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	claims := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(claims.UserID)
	tasks, err := s.Store.ListByUser(r.Context(), uid, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskOut(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	t, err := s.Store.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	claims := bauth.MustClaims(r.Context())
	if t.UserID.String() != claims.UserID {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return
	}
	s.cancelMu.Lock()
	if cancel, ok := s.cancelers[id]; ok {
		cancel()
		delete(s.cancelers, id)
	}
	s.cancelMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"task_id": id.String(), "cancel_requested": true})
}

// ─── helpers ────────────────────────────────────────────

func taskOut(t *store.Task) map[string]any {
	out := map[string]any{
		"id":              t.ID.String(),
		"agent":           t.Agent,
		"model":           t.Model,
		"status":          string(t.Status),
		"thread_id":       t.ThreadID,
		"run_id":          t.RunID,
		"topic":           "agui:run:" + t.RunID,
		"created_at":      t.CreatedAt.UTC().Format(time.RFC3339),
		"cost_tokens_in":  t.TokensIn,
		"cost_tokens_out": t.TokensOut,
		"cost_usd_micros": t.CostUSDMicros,
	}
	if t.ErrorMessage != "" {
		out["error_message"] = t.ErrorMessage
	}
	if t.StartedAt != nil {
		out["started_at"] = t.StartedAt.UTC().Format(time.RFC3339)
	}
	if t.FinishedAt != nil {
		out["finished_at"] = t.FinishedAt.UTC().Format(time.RFC3339)
	}
	return out
}

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
		r = r.WithContext(bauth.WithClaims(r.Context(), claims))
		next(w, r)
	}
}

// deriveAgentID computes a stable UUID for "this user's <label>
// agent". v5 in the OID namespace gives us a deterministic UUID
// without needing an agents table: every Run with the same
// (userID, label) pair joins to the same agent_skills rows. When a
// real agents registry lands, this function gets replaced with a
// lookup; the wire format stays UUID either way.
func deriveAgentID(userID uuid.UUID, label string) uuid.UUID {
	if label == "" {
		label = "biu"
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(userID.String()+":"+label))
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
