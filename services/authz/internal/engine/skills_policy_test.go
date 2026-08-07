package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// skillsPolicyPath resolves the canonical Skills policy file checked
// into deploy/docker-compose/authz/policies/. We test the literal
// file rather than a duplicated string constant so dev compose +
// production deploys can never drift from what these tests exercise.
func skillsPolicyPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// services/authz/internal/engine → repo root (4 levels up)
	root := filepath.Join(wd, "..", "..", "..", "..")
	p := filepath.Join(root, "deploy", "docker-compose", "authz", "policies", "policies.cedar")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("policy file %s not found: %v", p, err)
	}
	return p
}

func skillsEngine(t *testing.T) *Engine {
	t.Helper()
	raw, err := os.ReadFile(skillsPolicyPath(t))
	if err != nil {
		t.Fatal(err)
	}
	e := New()
	if err := e.LoadPolicies(raw); err != nil {
		t.Fatalf("load: %v", err)
	}
	return e
}

// makePrincipal + makeSkillResource — small builders so each case
// stays focused on the one or two attributes under test.

func userPrincipal(uid, orgID string) Entity {
	return Entity{
		Type: "User", ID: uid,
		Attributes: map[string]any{
			"id":     uid,
			"org_id": orgID,
		},
	}
}

func skillResource(skillID, orgID, ownerID, status string, perms []string) Entity {
	attrs := map[string]any{
		"id":          skillID,
		"org_id":      orgID,
		"owner_id":    ownerID,
		"status":      status,
		"permissions": stringSet(perms),
	}
	return Entity{Type: "Skill", ID: skillID, Attributes: attrs}
}

// Cedar's set semantics need a []any backing.
func stringSet(xs []string) []any {
	out := make([]any, len(xs))
	for i, s := range xs {
		out[i] = s
	}
	return out
}

func assertDecision(t *testing.T, res *Result, err error, want Decision, label string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if res.Decision != want {
		t.Errorf("%s: got %s, want %s; errors=%v", label, res.Decision, want, res.Errors)
	}
}

// ─── Read-only invocation ──────────────────────────────────

func TestSkillInvoke_AllowsActiveInOwnOrg(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:invoke",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", nil),
	})
	assertDecision(t, res, err, DecisionAllow, "invoke active own-org")
}

func TestSkillInvoke_DeniesCrossOrg(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:invoke",
		Resource:  skillResource("s-1", "org-B", "u-X", "active", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "cross-org invoke")
}

func TestSkillInvoke_DeniesDisabled(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:invoke",
		Resource:  skillResource("s-1", "org-A", "u-1", "disabled", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "invoke disabled")
}

// ─── sandbox.exec gate ─────────────────────────────────────

func TestSkillExecScript_AllowsWithPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:exec_script",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", []string{"sandbox.exec"}),
	})
	assertDecision(t, res, err, DecisionAllow, "exec_script with permission")
}

func TestSkillExecScript_DeniesWithoutPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:exec_script",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "exec_script without permission")
}

func TestSkillExecScript_DeniesWrongPermission(t *testing.T) {
	e := skillsEngine(t)
	// Skill declares network.fetch but not sandbox.exec.
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:exec_script",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", []string{"network.fetch"}),
	})
	assertDecision(t, res, err, DecisionDeny, "exec_script with wrong perm only")
}

// ─── network.fetch ─────────────────────────────────────────

func TestSkillFetchNetwork_AllowsWithPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:fetch_network",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", []string{"network.fetch"}),
	})
	assertDecision(t, res, err, DecisionAllow, "fetch_network with permission")
}

func TestSkillFetchNetwork_DeniesWithoutPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:fetch_network",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", []string{"sandbox.exec"}),
	})
	assertDecision(t, res, err, DecisionDeny, "fetch_network with only sandbox.exec")
}

// ─── wiki.read ─────────────────────────────────────────────

func TestSkillReadWiki_AllowsWithPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:read_wiki",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", []string{"wiki.read"}),
	})
	assertDecision(t, res, err, DecisionAllow, "read_wiki with permission")
}

func TestSkillReadWiki_DeniesWithoutPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:read_wiki",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "read_wiki without permission")
}

// ─── memory.recall ─────────────────────────────────────────

func TestSkillRecallMemory_AllowsWithPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:recall_memory",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", []string{"memory.recall"}),
	})
	assertDecision(t, res, err, DecisionAllow, "recall_memory with permission")
}

func TestSkillRecallMemory_DeniesWithoutPermission(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:recall_memory",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "recall_memory without permission")
}

// ─── export_file (no permission flag, but org-scoped) ──────

func TestSkillExportFile_AllowsForOwnOrg(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:export_file",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", nil),
	})
	assertDecision(t, res, err, DecisionAllow, "export_file own org")
}

func TestSkillExportFile_DeniesForCrossOrg(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:export_file",
		Resource:  skillResource("s-1", "org-B", "u-X", "active", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "export_file cross-org")
}

// ─── Self-authoring workflow ───────────────────────────────

func TestSkillPropose_AllowsAnyOrgMember(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:propose",
		Resource:  skillResource("s-1", "org-A", "u-1", "staged", nil),
	})
	assertDecision(t, res, err, DecisionAllow, "propose own-org")
}

func TestSkillApprove_AllowsOwnerOnStaged(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:approve",
		Resource:  skillResource("s-1", "org-A", "u-1", "staged", nil),
	})
	assertDecision(t, res, err, DecisionAllow, "approve own staged")
}

func TestSkillApprove_DeniesNonOwner(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-2", "org-A"), // not the owner
		Action:    "skill:approve",
		Resource:  skillResource("s-1", "org-A", "u-1", "staged", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "approve as non-owner")
}

func TestSkillApprove_DeniesAlreadyActive(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:approve",
		Resource:  skillResource("s-1", "org-A", "u-1", "active", nil),
	})
	assertDecision(t, res, err, DecisionDeny, "approve already-active")
}

// ─── Hard denies (forbid wins) ─────────────────────────────

func TestSkillSuspended_DeniesEvenWithPermissions(t *testing.T) {
	e := skillsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipal("u-1", "org-A"),
		Action:    "skill:exec_script",
		Resource: skillResource("s-1", "org-A", "u-1", "suspended",
			[]string{"sandbox.exec", "network.fetch", "wiki.read"}),
	})
	assertDecision(t, res, err, DecisionDeny, "suspended with all perms")
}
