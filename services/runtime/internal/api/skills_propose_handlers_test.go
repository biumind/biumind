package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/runtime/internal/authz"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/google/uuid"
)

// State-machine validation. Pinned by table — every other allowed
// transition lives in Skills-Design §11A.4 and changing this table
// without updating the doc is a contract break.
func TestValidateTransition(t *testing.T) {
	cases := []struct {
		from, to skillsreg.Status
		wantOK   bool
	}{
		// Staged → terminal verbs OK
		{skillsreg.StatusStaged, skillsreg.StatusActive, true},
		{skillsreg.StatusStaged, skillsreg.StatusDisabled, true},
		// Staged → not-allowed
		{skillsreg.StatusStaged, skillsreg.StatusStagedOrg, false},
		{skillsreg.StatusStaged, skillsreg.StatusSuspended, false},

		// Active → disable / share-org OK
		{skillsreg.StatusActive, skillsreg.StatusDisabled, true},
		{skillsreg.StatusActive, skillsreg.StatusStagedOrg, true},
		// Active → can't go back to staged
		{skillsreg.StatusActive, skillsreg.StatusStaged, false},

		// Disabled is terminal in v1.5
		{skillsreg.StatusDisabled, skillsreg.StatusActive, false},
		{skillsreg.StatusDisabled, skillsreg.StatusStaged, false},

		// staged_org → admin approve / reject
		{skillsreg.StatusStagedOrg, skillsreg.StatusActive, true},
		{skillsreg.StatusStagedOrg, skillsreg.StatusDisabled, true},

		// Suspended is platform-level — no user transitions out
		{skillsreg.StatusSuspended, skillsreg.StatusActive, false},

		// Self-loops rejected (callers should pick a different verb)
		{skillsreg.StatusStaged, skillsreg.StatusStaged, false},
	}
	for _, c := range cases {
		err := validateTransition(c.from, c.to)
		ok := err == nil
		if ok != c.wantOK {
			t.Errorf("validateTransition(%s → %s) = (ok=%v, %v); want ok=%v",
				c.from, c.to, ok, err, c.wantOK)
		}
		if !ok && !errors.Is(err, errBadTransition) {
			t.Errorf("invalid transitions must wrap errBadTransition; got %v", err)
		}
	}
}

// Cedar gate — every propose-flow handler delegates ownership /
// state-machine constraints to authz. The pure-handler test below
// drives authzCheckSkill against a recording stub: confirms (a) the
// principal+resource shape is what the .cedar policy expects, and
// (b) deny / err paths translate to 403 / 502 respectively (so a
// future Authz wiring change can't silently soften the failure mode).
type stubDecider struct {
	called   atomic.Int32
	last     authz.Request
	decision authz.Decision
	err      error
}

func (s *stubDecider) Check(_ context.Context, req authz.Request) (*authz.Result, error) {
	s.called.Add(1)
	s.last = req
	if s.err != nil {
		return nil, s.err
	}
	return &authz.Result{Decision: s.decision, Reason: "stub"}, nil
}

func TestAuthzCheckSkill_AllowPath(t *testing.T) {
	dec := &stubDecider{decision: authz.Allow}
	srv := &Server{SkillsAuthz: dec}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/x/approve", nil)
	owner := uuid.New()
	org := uuid.New()
	sk := &skillsreg.Skill{
		ID: "skill_x", OrgID: org, OwnerID: &owner,
		Status: skillsreg.StatusStaged, Source: skillsreg.SourceUser,
		Permissions: []string{"sandbox.exec"},
	}
	ok := srv.authzCheckSkill(rr, req, "skill:approve", owner, org, sk)
	if !ok {
		t.Fatalf("Allow should pass; got %d %s", rr.Code, rr.Body.String())
	}
	if dec.called.Load() != 1 {
		t.Errorf("decider not called")
	}
	// Resource shape — the literal field names below are the contract
	// the .cedar file reads. Don't rename without updating policy.
	attrs := dec.last.Resource.Attributes
	if attrs["org_id"] != org.String() {
		t.Errorf("org_id missing or wrong: %v", attrs["org_id"])
	}
	if attrs["owner_id"] != owner.String() {
		t.Errorf("owner_id missing or wrong: %v", attrs["owner_id"])
	}
	if attrs["status"] != "staged" {
		t.Errorf("status not propagated: %v", attrs["status"])
	}
	perms, _ := attrs["permissions"].([]any)
	if len(perms) != 1 || perms[0] != "sandbox.exec" {
		t.Errorf("permissions not flattened to []any: %v", attrs["permissions"])
	}
	if dec.last.Action != "skill:approve" {
		t.Errorf("action: %s", dec.last.Action)
	}
	if dec.last.Principal.Attributes["org_id"] != org.String() {
		t.Errorf("principal.org_id missing")
	}
}

func TestAuthzCheckSkill_DenyReturns403(t *testing.T) {
	dec := &stubDecider{decision: authz.Deny}
	srv := &Server{SkillsAuthz: dec}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/x/approve", nil)
	sk := &skillsreg.Skill{ID: "skill_x", OrgID: uuid.New(), Status: skillsreg.StatusStaged}
	if srv.authzCheckSkill(rr, req, "skill:approve", uuid.New(), sk.OrgID, sk) {
		t.Fatal("Deny should not pass")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "forbidden") {
		t.Errorf("body should carry forbidden code: %s", rr.Body.String())
	}
}

func TestAuthzCheckSkill_ErrReturns502(t *testing.T) {
	dec := &stubDecider{err: errors.New("boom")}
	srv := &Server{SkillsAuthz: dec}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/x/approve", nil)
	sk := &skillsreg.Skill{ID: "skill_x", OrgID: uuid.New()}
	if srv.authzCheckSkill(rr, req, "skill:approve", uuid.New(), sk.OrgID, sk) {
		t.Fatal("transport err must fail-closed")
	}
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}

func TestAuthzCheckSkill_NilDeciderFallsBackToAlwaysAllow(t *testing.T) {
	// Dev / CLI mode parity with skill_tools.go decider() — nil
	// SkillsAuthz must not block routes outright (the daemon logs a
	// warning at startup). This is a deliberate fail-open behaviour;
	// production must wire authz.NewHTTP.
	srv := &Server{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/x/approve", nil)
	sk := &skillsreg.Skill{ID: "skill_x", OrgID: uuid.New()}
	if !srv.authzCheckSkill(rr, req, "skill:approve", uuid.New(), sk.OrgID, sk) {
		t.Fatalf("nil decider should fall through to AlwaysAllow; got %d %s",
			rr.Code, rr.Body.String())
	}
}

// proposeBody is the minimal valid JSON the propose handler accepts.
func proposeBody(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(proposeSkillReq{
		Identifier:  "p-1",
		Name:        "P",
		Description: "d",
		Body:        "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Cedar gate is invoked on the propose flow with the synthesised
// resource shape (OrgID + OwnerID + Status=staged). Drives a Deny
// decider and asserts the handler does NOT reach the registry —
// otherwise an Authz misconfig would leak rows.
func TestHandleProposeSkill_DeniedByAuthz(t *testing.T) {
	dec := &stubDecider{decision: authz.Deny}
	srv := &Server{
		Skills:      &skillsreg.Registry{}, // never reached when denied
		SkillsAuthz: dec,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/propose",
		strings.NewReader(string(proposeBody(t))))
	req = req.WithContext(bauth.WithClaims(req.Context(), &bauth.Claims{
		UserID: uuid.NewString(), OrgID: uuid.NewString(),
	}))
	rr := httptest.NewRecorder()
	srv.handleProposeSkill(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if dec.called.Load() != 1 {
		t.Errorf("authz decider not called: %d", dec.called.Load())
	}
	if dec.last.Action != "skill:propose" {
		t.Errorf("action = %q, want skill:propose", dec.last.Action)
	}
	if dec.last.Resource.Attributes["status"] != "staged" {
		t.Errorf("status not staged: %v", dec.last.Resource.Attributes["status"])
	}
}

// Mount with non-nil Skills wires the four propose-flow routes.
// We don't drive a registry here — Mount just needs to register the
// handlers without crashing; full integration coverage lands when
// DATABASE_URL is wired.
func TestMount_RegistersProposeRoutes(t *testing.T) {
	srv := &Server{Skills: &skillsreg.Registry{}}
	mux := newMuxFor(t, srv)

	for _, path := range []string{
		"/v1/skills/propose",
		"/v1/skills/abc/approve",
		"/v1/skills/abc/reject",
		"/v1/skills/abc/share-org",
	} {
		// Each path should resolve to a handler that the existing
		// requireAuth wrapper rejects with 401 (no Bearer header
		// in the test request) — confirms the route is mounted.
		// 404 here would mean the route was never registered.
		if got := postNoAuth(t, mux, path); got != 401 {
			t.Errorf("POST %s → status %d, want 401 (route registered, auth missing)",
				path, got)
		}
	}
}
