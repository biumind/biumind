package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/google/uuid"
)

// Pure-function checks — the DB-backed integration tests for these
// handlers live alongside skills/registry_test.go; here we cover the
// translation layer only.

func TestMustOrgID_FailsWithoutOrgClaim(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/skills", nil)
	req = req.WithContext(bauth.WithClaims(context.Background(),
		&bauth.Claims{UserID: uuid.NewString()},
	))
	rr := httptest.NewRecorder()
	if _, ok := mustOrgID(rr, req); ok {
		t.Error("expected ok=false when claim is missing")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no_org") {
		t.Errorf("body should carry no_org code: %s", rr.Body.String())
	}
}

func TestMustOrgID_FailsOnGarbageOrgClaim(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/skills", nil)
	req = req.WithContext(bauth.WithClaims(context.Background(),
		&bauth.Claims{UserID: uuid.NewString(), OrgID: "not-a-uuid"},
	))
	rr := httptest.NewRecorder()
	if _, ok := mustOrgID(rr, req); ok {
		t.Error("expected ok=false on bad org_id")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestMustOrgID_HappyPath(t *testing.T) {
	want := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/skills", nil)
	req = req.WithContext(bauth.WithClaims(context.Background(),
		&bauth.Claims{UserID: uuid.NewString(), OrgID: want.String()},
	))
	rr := httptest.NewRecorder()
	got, ok := mustOrgID(rr, req)
	if !ok {
		t.Fatalf("expected ok=true; status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got != want {
		t.Errorf("org id = %s, want %s", got, want)
	}
}

func TestSkillToJSON_OmitsEmptyOptionalFields(t *testing.T) {
	owner := uuid.New()
	s := &skillsreg.Skill{
		ID:          "skill_x",
		OrgID:       uuid.New(),
		OwnerID:     &owner,
		Identifier:  "foo",
		Name:        "Foo",
		Source:      skillsreg.SourceUser,
		Status:      skillsreg.StatusActive,
		ContentHash: "abc",
	}
	out := skillToJSON(s)
	if _, has := out["zip_file_sha256"]; has {
		t.Error("empty zip_file_sha256 must not serialise")
	}
	if got := out["owner_id"]; got != owner.String() {
		t.Errorf("owner_id = %v, want %s", got, owner)
	}
}

func TestSkillToJSON_SkipsNilOwner(t *testing.T) {
	s := &skillsreg.Skill{
		ID:     "skill_y",
		OrgID:  uuid.New(),
		Source: skillsreg.SourceOrg,
		Status: skillsreg.StatusActive,
	}
	out := skillToJSON(s)
	if _, has := out["owner_id"]; has {
		t.Error("nil OwnerID must not serialise — confuses clients into showing a placeholder")
	}
}

func TestNewSkillID_FormatStable(t *testing.T) {
	a := newSkillID()
	b := newSkillID()
	if a == b {
		t.Error("newSkillID must produce unique IDs")
	}
	for _, id := range []string{a, b} {
		if !strings.HasPrefix(id, "skill_") {
			t.Errorf("missing prefix: %q", id)
		}
		// 16 random bytes → 32 hex digits → 38 char ID total.
		if len(id) != len("skill_")+32 {
			t.Errorf("len(%q) = %d, want %d", id, len(id), len("skill_")+32)
		}
	}
}

// newMuxFor + postNoAuth are shared helpers used by
// skills_propose_handlers_test.go to confirm Mount registers the
// new self-authoring routes.
func newMuxFor(t *testing.T, srv *Server) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	srv.Mount(mux)
	return mux
}

func postNoAuth(t *testing.T, mux *http.ServeMux, path string) int {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, nil))
	return rr.Code
}

// Smoke check that Mount actually registers the routes when Skills
// is wired (and skips them when nil).
func TestMount_GatesSkillsRoutesOnRegistry(t *testing.T) {
	// nil → no skills routes.
	mux := http.NewServeMux()
	srv := &Server{}
	srv.Mount(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/v1/skills", nil))
	// Without auth header the requireAuth wrapper would 401; without
	// the handler registered the mux returns 404. We expect 404 to
	// confirm the route isn't mounted.
	if rr.Code != http.StatusNotFound {
		t.Errorf("nil registry should leave route unmounted; got %d", rr.Code)
	}

	// non-nil → route IS mounted (even if Registry calls would fail
	// for lack of a DB; we just confirm the path is reachable).
	mux2 := http.NewServeMux()
	srv2 := &Server{Skills: &skillsreg.Registry{}}
	srv2.Mount(mux2)
	rr2 := httptest.NewRecorder()
	mux2.ServeHTTP(rr2,
		httptest.NewRequest(http.MethodGet, "/v1/skills", nil))
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("registry wired but no auth → want 401, got %d body=%s",
			rr2.Code, rr2.Body.String())
	}
}

// JSON request shape sanity — pin the wire format so a future
// proto regen can't silently drift the JSON keys clients rely on.
func TestInstallSkillReq_KnownKeys(t *testing.T) {
	req := installSkillReq{}
	body, _ := json.Marshal(req)
	for _, key := range []string{
		`"identifier"`, `"name"`, `"description"`, `"body"`,
		`"manifest"`, `"paths"`, `"permissions"`, `"resources"`,
		`"target_agent_id"`, `"pin"`,
	} {
		if !strings.Contains(string(body), key) {
			t.Errorf("missing JSON key %s in zero-value marshal: %s", key, body)
		}
	}
}
