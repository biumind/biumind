// Self-authoring workflow handlers (PS3.1):
//
//   POST /v1/skills/propose            create draft (status=staged)
//   POST /v1/skills/{id}/approve       staged → active
//   POST /v1/skills/{id}/reject        staged → disabled
//   POST /v1/skills/{id}/share-org     active → staged_org (admin awaits)
//
// State machine — validated server-side so a misconfigured client
// can't smuggle a row from disabled back to active. The full graph
// lives in docs/BiuMind-Skills-Design.md §11A.4; the
// validateTransition table here pins it.
//
// Authorization: every mutation here calls Authz.Check against the
// policies.cedar policy file. Owner-only / status-gating /
// state-machine rules live entirely in the .cedar file (I9 — business
// code carries zero authorization logic). When AUTHZ_URL is unset the
// runtime falls back to authz.AlwaysAllow with a startup warning;
// production daemons MUST wire the HTTP decider.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/runtime/internal/authz"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/google/uuid"
)

// ─── Propose ────────────────────────────────────────────────

type proposeSkillReq struct {
	Identifier  string                            `json:"identifier"`
	Name        string                            `json:"name"`
	Description string                            `json:"description"`
	Body        string                            `json:"body"`
	Manifest    skillsreg.Manifest                `json:"manifest"`
	Paths       []string                          `json:"paths"`
	Permissions []string                          `json:"permissions"`
	Resources   map[string]skillsreg.ResourceMeta `json:"resources"`
	// UpdateOf — when set, the propose request is a v2-of an existing
	// skill. Server diffs body+resources and surfaces both versions
	// to the approver via the Realtime payload (PS3.5).
	UpdateOf string `json:"update_of"`
}

func (s *Server) handleProposeSkill(w http.ResponseWriter, r *http.Request) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return
	}
	uid, err := uuid.Parse(bauth.MustClaims(r.Context()).UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	var req proposeSkillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Identifier == "" || req.Name == "" || req.Description == "" || req.Body == "" {
		writeErr(w, http.StatusBadRequest, "missing_field",
			"identifier, name, description, body all required")
		return
	}
	// Cedar gate — synthesise the not-yet-persisted Skill resource so
	// the policy can rule on org_id + (future) per-org propose quotas.
	// owner_id is set to the caller; status is StatusStaged because
	// that's the row's about-to-be value.
	synth := &skillsreg.Skill{
		OrgID:   orgID,
		OwnerID: &uid,
		Status:  skillsreg.StatusStaged,
		Source:  skillsreg.SourceUser,
	}
	if !s.authzCheckSkill(w, r, "skill:propose", uid, orgID, synth) {
		return
	}

	in := skillsreg.CreateInput{
		ID:          newSkillID(),
		OrgID:       orgID,
		OwnerID:     &uid,
		Identifier:  req.Identifier,
		Name:        req.Name,
		Description: req.Description,
		Source:      skillsreg.SourceUser,
		Manifest:    req.Manifest,
		Content:     req.Body,
		Resources:   req.Resources,
		Paths:       req.Paths,
		Permissions: req.Permissions,
		Status:      skillsreg.StatusStaged,
		UpdateOfID:  req.UpdateOf, // persist the predecessor pointer
	}
	sk, err := s.Skills.Create(r.Context(), in)
	if err != nil {
		mapSkillErr(w, err)
		return
	}

	out := skillToJSON(sk)
	if req.UpdateOf != "" {
		// Surface the upstream skill (if it exists in the same org)
		// so the approver UI can render a diff. Best-effort — a
		// missing target isn't fatal; treat propose as a fresh
		// rather than 404 so the user's draft isn't lost.
		if prev, err := s.Skills.Get(r.Context(), req.UpdateOf); err == nil && prev.OrgID == orgID {
			out["update_of"] = map[string]any{
				"id":           prev.ID,
				"identifier":   prev.Identifier,
				"content_hash": prev.ContentHash,
				"content":      prev.Content,
			}
		}
	}

	// Publish a Realtime event so the org's approver UI can light up
	// without polling. Topic is per-org, not per-run, so admins
	// subscribed to "org:<org_id>:skill_proposals" get every staged
	// row across users.
	s.publishSkillEvent(r.Context(), orgID, sk.ID, "biumind.runtime.skill.proposed", map[string]any{
		"skill_id":   sk.ID,
		"identifier": sk.Identifier,
		"name":       sk.Name,
		"owner_id":   uid.String(),
		"update_of":  req.UpdateOf,
	})
	writeJSON(w, http.StatusOK, out)
}

// ─── Approve / Reject / ShareOrg ────────────────────────────

type approveSkillReq struct {
	// EnableOnDefaultAgent — true means "as soon as it's active,
	// pin it on the caller's deriveAgentID()-defined default agent."
	// Mirrors the install flow's target_agent_id ergonomics.
	EnableOnDefaultAgent bool `json:"enable_on_default_agent"`
}

func (s *Server) handleApproveSkill(w http.ResponseWriter, r *http.Request) {
	id, sk, ok := s.lookupAndAuthorize(w, r, "skill:approve")
	if !ok {
		return
	}
	if err := validateTransition(skillsreg.Status(sk["status"].(string)), skillsreg.StatusActive); err != nil {
		writeErr(w, http.StatusConflict, "bad_transition", err.Error())
		return
	}
	var req approveSkillReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	out, err := s.Skills.SetStatus(r.Context(), id, skillsreg.StatusActive, "approved")
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	if req.EnableOnDefaultAgent {
		uid, err := uuid.Parse(bauth.MustClaims(r.Context()).UserID)
		if err == nil {
			agent := deriveAgentID(uid, "biu")
			_, _ = s.Skills.Toggle(r.Context(), agent, id, true, true)
		}
	}
	s.publishSkillEvent(r.Context(), out.OrgID, out.ID, "biumind.runtime.skill.approved", map[string]any{
		"skill_id":   out.ID,
		"identifier": out.Identifier,
	})
	writeJSON(w, http.StatusOK, skillToJSON(out))
}

type rejectSkillReq struct {
	Reason string `json:"reason"`
}

func (s *Server) handleRejectSkill(w http.ResponseWriter, r *http.Request) {
	id, sk, ok := s.lookupAndAuthorize(w, r, "skill:reject")
	if !ok {
		return
	}
	if err := validateTransition(skillsreg.Status(sk["status"].(string)), skillsreg.StatusDisabled); err != nil {
		writeErr(w, http.StatusConflict, "bad_transition", err.Error())
		return
	}
	var req rejectSkillReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	out, err := s.Skills.SetStatus(r.Context(), id, skillsreg.StatusDisabled, req.Reason)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	s.publishSkillEvent(r.Context(), out.OrgID, out.ID, "biumind.runtime.skill.rejected", map[string]any{
		"skill_id":   out.ID,
		"identifier": out.Identifier,
		"reason":     req.Reason,
	})
	writeJSON(w, http.StatusOK, skillToJSON(out))
}

func (s *Server) handleShareSkillOrg(w http.ResponseWriter, r *http.Request) {
	id, sk, ok := s.lookupAndAuthorize(w, r, "skill:share_org")
	if !ok {
		return
	}
	if err := validateTransition(skillsreg.Status(sk["status"].(string)), skillsreg.StatusStagedOrg); err != nil {
		writeErr(w, http.StatusConflict, "bad_transition", err.Error())
		return
	}
	out, err := s.Skills.SetStatus(r.Context(), id, skillsreg.StatusStagedOrg, "share_org")
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	s.publishSkillEvent(r.Context(), out.OrgID, out.ID, "biumind.runtime.skill.shared", map[string]any{
		"skill_id":   out.ID,
		"identifier": out.Identifier,
	})
	writeJSON(w, http.StatusOK, skillToJSON(out))
}

// ─── helpers ────────────────────────────────────────────────

// lookupAndAuthorize loads the skill by URL path, hands the row to
// Cedar for the requested action, and returns the JSON-shaped row
// alongside the parsed id. Returns (id, sk-as-map, true) on success;
// on failure writes the response and returns (_, _, false) so the
// caller can early-out cleanly.
//
// Cross-org reads surface as 404 (not 403) to avoid leaking existence
// to outsiders; that one rule still lives here because it's an HTTP
// concern (status code shape), not an authorization rule. Owner /
// status / state-machine constraints all live in policies.cedar.
func (s *Server) lookupAndAuthorize(w http.ResponseWriter, r *http.Request, action string) (string, map[string]any, bool) {
	orgID, ok := mustOrgID(w, r)
	if !ok {
		return "", nil, false
	}
	id := r.PathValue("id")
	cur, err := s.Skills.Get(r.Context(), id)
	if err != nil {
		mapSkillErr(w, err)
		return "", nil, false
	}
	if cur.OrgID != orgID {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return "", nil, false
	}
	uid, err := uuid.Parse(bauth.MustClaims(r.Context()).UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return "", nil, false
	}
	if !s.authzCheckSkill(w, r, action, uid, orgID, cur) {
		return "", nil, false
	}
	return id, skillToJSON(cur), true
}

// authzCheckSkill calls the Cedar decider for `action` on `sk` as
// viewed by `principal`. Writes a 403 + reason on Deny, a 502 on
// transport failure (fail-closed), and returns true only on Allow.
//
// Centralised so the propose / approve / reject / share-org / install
// flows all marshal the same Cedar resource shape — drift here would
// be a silent policy bypass.
func (s *Server) authzCheckSkill(w http.ResponseWriter, r *http.Request, action string, principal uuid.UUID, orgID uuid.UUID, sk *skillsreg.Skill) bool {
	d := s.SkillsAuthz
	if d == nil {
		// Same fail-open semantics as agent.SkillToolDeps.decider() —
		// dev / CLI mode without an Authz daemon. The runtime daemon
		// logs a startup warning when AUTHZ_URL is unset.
		d = authz.AlwaysAllow{}
	}
	ownerID := ""
	if sk.OwnerID != nil {
		ownerID = sk.OwnerID.String()
	}
	res, err := d.Check(r.Context(), authz.Request{
		Principal: authz.Entity{
			Type: "User",
			ID:   principal.String(),
			Attributes: map[string]any{
				"id":     principal.String(),
				"org_id": orgID.String(),
			},
		},
		Action: action,
		Resource: authz.Entity{
			Type: "Skill",
			ID:   sk.ID,
			Attributes: map[string]any{
				"id":          sk.ID,
				"org_id":      sk.OrgID.String(),
				"owner_id":    ownerID,
				"status":      string(sk.Status),
				"source":      string(sk.Source),
				"permissions": skillPermsAsAny(sk.Permissions),
			},
		},
	})
	if err != nil {
		// Fail-closed — never silently allow on Authz outage. 502
		// because this is an upstream-dependency failure from the
		// caller's perspective.
		writeErr(w, http.StatusBadGateway, "authz_unreachable", err.Error())
		return false
	}
	if res.Decision != authz.Allow {
		writeErr(w, http.StatusForbidden, "forbidden", res.Reason)
		return false
	}
	return true
}

func skillPermsAsAny(xs []string) []any {
	out := make([]any, len(xs))
	for i, s := range xs {
		out[i] = s
	}
	return out
}

// validateTransition encodes the allowed state graph from
// Skills-Design §11A.4. Self-loops are no-ops at the
// registry level; here we surface them as bad-transition so the
// caller can pick a different verb instead of paying the audit
// emit cost twice.
func validateTransition(from, to skillsreg.Status) error {
	allowed := map[skillsreg.Status][]skillsreg.Status{
		skillsreg.StatusStaged: {
			skillsreg.StatusActive,   // approve
			skillsreg.StatusDisabled, // reject
		},
		skillsreg.StatusStagedOrg: {
			skillsreg.StatusActive,   // admin approve
			skillsreg.StatusDisabled, // admin reject
		},
		skillsreg.StatusActive: {
			skillsreg.StatusDisabled,  // owner_disable
			skillsreg.StatusStagedOrg, // share-org
		},
		skillsreg.StatusDisabled: {
			// Disabled rows are terminal in v1.5. To revive, propose
			// a new draft via update_of.
		},
		skillsreg.StatusSuspended: {
			// Platform-level only; user actions don't get out of suspended.
		},
	}
	for _, ok := range allowed[from] {
		if ok == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", errBadTransition, from, to)
}

var errBadTransition = errors.New("invalid status transition")

// publishSkillEvent — best-effort Realtime fanout. Failure to
// publish doesn't fail the HTTP response (the skill row is already
// committed; an undeliverable notification is a UX nit, not an
// integrity bug). The events row in brain.events still records
// the state change for replay.
//
// Topic shape: `org:<org_id>:skill_events` so org admins / the
// proposer's UI can subscribe once and pick up every state change
// in their org without enumerating skill_ids. Per-skill events also
// land on `skill:<skill_id>` so the detail page can tail one row.
// publishSkillEvent —— S11-4 后只 log + DB 落 events 表。原 AG-UI
// realtime fanout 路径已删；org 级 skill_events 后续走 brain agent_plane
// 自定义事件（SDK Protocol system_status 帧 + 订阅广播），等需求来了
// 再实现。当前 propose / approve 等 DB 写入仍 durable，replay 可从
// events 表重建。
func (s *Server) publishSkillEvent(_ context.Context, orgID uuid.UUID, skillID, eventType string, payload map[string]any) {
	if s.Logger != nil {
		s.Logger.Debug("skill event recorded (live fanout path removed in S11-4)",
			"event_type", eventType,
			"org_id", orgID.String(), "skill_id", skillID,
			"payload_keys", len(payload))
	}
}
