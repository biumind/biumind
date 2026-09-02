package api

// agent_run.go — S3 P0-1 wiki autonomous-maintenance agent loop HTTP entry.
//
// POST /v1/wiki/projects/{pid}/agent/run streams an LLM tool loop that reads
// sources / mutates pages / maintains backlinks inside one project. It is the
// biumind counterpart of reference/llm_wiki's agent runtime.rs — but reuses
// the existing chat AgentLoop (function-calling, not ReAct) with a wider
// tool whitelist (tools.WikiAgentToolAllowlist) and a wiki-specific system
// prompt + iteration budget.
//
// See docs/BiuMind-S3-AgentLoop-Design.md §7.1.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// handleWikiAgentRun — POST /v1/wiki/projects/{pid}/agent/run.
//
// Auth + project ownership are checked up front (requireAuth + ownsProject).
// The loop's write tools re-check ownership on every call (checkProjectOwned),
// so pid is guidance (which project to work in) + a routing artifact, not a
// security boundary. No chat thread / message rows are written — the run is
// task-scoped and its effect surfaces as page.created / page.updated events
// on the page stream (the store emits those inside each tool's tx).
//
// Once RunAgentLoop opens the SSE stream (200 + headers) it owns the writer;
// failures after that point are surfaced as block.error / MessageError events,
// NOT via writeErr (headers are already sent). We only log here.
//
// On success the handler fires a server-side semantic lint scan
// (triggerSemanticScan) so contradiction findings persist even if the
// client disconnects right after the stream closes.
func (s *Server) handleWikiAgentRun(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	if s.Relay == nil {
		writeErr(w, http.StatusServiceUnavailable, "agent_disabled",
			"model-relay not configured (MODEL_RELAY_URL unset)")
		return
	}
	var req wikiAgentRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Instruction) == "" {
		writeErr(w, http.StatusBadRequest, "empty_instruction",
			"instruction is required")
		return
	}
	if req.Model == "" {
		writeErr(w, http.StatusBadRequest, "no_model",
			"model is required (the agent loop has no thread default to fall back on)")
		return
	}

	uid := mustUserID(r)
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = uuid.NewString()
	}
	// Tag the loop ctx with the caller's user id so write-tool Invokers can
	// owner-scope (tools.UserIDFromContext). Detached from the request ctx
	// so a client disconnect doesn't kill the in-flight model-relay stream
	// (same pattern as chat HandleSend). Cancellation therefore goes
	// through the dedicated /agent/run/cancel endpoint, which looks up
	// runID in AgentRuns and calls the cancel func stored below.
	hubCtx, cancel := context.WithCancel(tools.WithUserID(context.Background(), uid))
	defer cancel()
	s.AgentRuns.Store(runID, cancel)
	defer s.AgentRuns.Delete(runID)

	system := wikiAgentSystemPrompt(pid)
	maxTurns := wikiAgentMaxTurns(req.Mode)

	if _, err := s.Relay.RunAgentLoop(hubCtx, w, r, chat.AgentLoopRunInput{
		System:    system,
		UserText:  req.Instruction,
		Model:     req.Model,
		Allowlist: tools.WikiAgentToolAllowlist,
		MaxTurns:  maxTurns,
	}); err != nil {
		if s.Logger != nil {
			s.Logger.WarnContext(r.Context(), "wiki agent run failed",
				"project_id", pid, "user_id", uid, "err", err)
		}
		return
	}
	// S3 P1: agent run succeeded → fire a semantic lint scan server-side.
	// Before this hook the scan was triggered by the client (maintain_dialog)
	// after deep runs only — a crashed / closed client lost it entirely.
	s.triggerSemanticScan(pid, uid)
}

// triggerSemanticScan fires one semantic lint scan in a detached goroutine.
// Called after RunAgentLoop returns success — the SSE stream is finished at
// that point, so the scan never blocks the response.
//
// All modes (fast / standard / deep) trigger: the scan is a single LLM call
// over ≤60 page summaries, and SemanticRunner is safe against duplicates —
// per-project inflight guard (ErrSemanticAlreadyRunning) plus
// review_items.dedupe_key UNIQUE make re-fires idempotent. nil Semantic
// (model-relay / JWT unconfigured) ⇒ silent no-op.
func (s *Server) triggerSemanticScan(projectID, ownerID uuid.UUID) {
	if s.Semantic == nil {
		return
	}
	go func() {
		if err := s.Semantic.Run(context.Background(), projectID, ownerID); err != nil && s.Logger != nil {
			s.Logger.Warn("wiki agent: post-run semantic scan failed",
				"project_id", projectID, "err", err)
		}
	}()
}

// handleWikiAgentCancel — POST /v1/wiki/projects/{pid}/agent/run/cancel.
//
// Stops a previously started agent run by run_id. The run's hubCtx is
// deliberately detached from the request ctx (see handleWikiAgentRun), so
// the client simply closing the SSE stream does NOT stop the loop — only
// this endpoint does. Auth + project ownership are re-checked so a foreign
// user can't cancel someone else's run (run ids are unguessable UUIDs, but
// defense in depth). 200 is returned once cancel() is invoked; the loop
// then exits at the next cancel-point (best-effort: an in-flight model-relay
// turn finishes first). 404 if no run is registered under run_id — already
// finished, never existed, or cancelled before (LoadAndDelete is idempotent
// so a duplicate cancel gets 404, which is fine).
func (s *Server) handleWikiAgentCancel(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	var req wikiAgentCancelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		writeErr(w, http.StatusBadRequest, "empty_run_id", "")
		return
	}
	v, ok := s.AgentRuns.LoadAndDelete(runID)
	if !ok {
		writeErr(w, http.StatusNotFound, "run_not_found",
			"no in-flight agent run for this run_id (already finished, cancelled, or unknown)")
		return
	}
	v.(context.CancelFunc)()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run_id": runID})
}

type wikiAgentCancelReq struct {
	RunID string `json:"run_id"`
}

type wikiAgentRunReq struct {
	// RunID is the client-chosen id for this run, used to cancel it via
	// POST .../agent/run/cancel. Empty → server generates one (but then
	// the caller can't cancel by id, so clients should always send one).
	RunID string `json:"run_id"`
	// Mode is the iteration budget tier: fast|standard|deep (S3 分叉 3=B).
	// Unknown / empty → standard. See wikiAgentMaxTurns.
	Mode string `json:"mode"`
	// Instruction is the single user-turn task, e.g.
	// "整理这个项目的知识，补全缺失页，合并明显重复".
	Instruction string `json:"instruction"`
	// Model is the model id resolved by model-relay. Required — unlike chat
	// (which falls back to thread.Model), the wiki agent has no thread.
	Model string `json:"model"`
}

// wikiAgentMaxTurns maps mode → loop iteration budget (S3 分叉 3=B).
// Tiers: Fast=4/Standard=8/Deep=12. Unknown / empty mode → standard (8).
func wikiAgentMaxTurns(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "fast":
		return 4
	case "deep":
		return 12
	default: // standard / empty / unknown
		return 8
	}
}

// wikiAgentSystemPrompt builds the LLM system prompt for one wiki maintenance
// run. Carries the project id (the agent works inside it) + the
// read-source → mutate-page → maintain-backlinks → surface-contradictions
// directives. biumind uses native function-calling (tool list injected by
// the agent loop), NOT reference's ReAct JSON protocol.
func wikiAgentSystemPrompt(pid uuid.UUID) string {
	return fmt.Sprintf(`You are the BiuMind wiki autonomous-maintenance agent for project %s.

You operate a tool loop: read existing pages and sources, then mutate pages to keep the wiki coherent. Your write tools are wiki_create_page, wiki_update_page, wiki_merge_pages; read tools include wiki_search, websearch, memory_recall.

Guidelines:
- Work ONLY inside project %s. Every write tool re-checks ownership and uses version乐观锁 (If-Match); a stale or foreign write is rejected with an error — re-read the page, do not retry blindly.
- Default to creating NEW pages (wiki_create_page). To change an existing page you MUST first search/read it to learn its current version, then call wiki_update_page with that version. Never blindly overwrite.
- Maintain backlinks implicitly: when creating a page, link to related existing pages with [[wikilinks]] in body_md; the graph rebuilds from links.
- If you find contradictory claims across pages, do NOT silently pick one. Finish the requested task, then surface the contradiction (with page ids) in your final summary so the user can review it.
- Every mutation is snapshotted to page_revisions automatically, so mistakes are reversible. Prefer a correct small change over a large speculative rewrite.
- Stop when the instruction is satisfied. Cite page ids + versions in your final summary.`,
		pid, pid)
}
