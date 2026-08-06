package rss

import (
	"context"
	"errors"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// stubAuthz records the last (write) it was asked about and returns a
// fixed verdict, letting us assert resolveScope wires the gate without
// a live Authz service.
type stubAuthz struct {
	allow     bool
	lastWrite bool
	calls     int
}

func (s *stubAuthz) AuthorizeOrg(_ context.Context, _ *bauth.Claims, write bool) error {
	s.calls++
	s.lastWrite = write
	if s.allow {
		return nil
	}
	return ErrOrgForbidden
}

func ctxClaims(c *bauth.Claims) context.Context {
	return bauth.WithClaims(context.Background(), c)
}

func TestResolveScope_UserScope_NoAuthz(t *testing.T) {
	// No authz wired; user scope must still work (a user owns their data)
	// and must NOT consult the authorizer.
	a := &App{}
	ctx := ctxClaims(&bauth.Claims{UserID: "u1"})
	for _, req := range []string{"", "user"} {
		scope, id, err := a.resolveScope(ctx, req, false)
		if err != nil {
			t.Fatalf("req=%q: %v", req, err)
		}
		if scope != "user" || id != "u1" {
			t.Fatalf("req=%q: got (%s,%s)", req, scope, id)
		}
	}
}

func TestResolveScope_OrgWithoutAuthz_FailsClosed(t *testing.T) {
	// org requested but no authz checker → ErrOrgScopeUnavailable, never
	// a silent downgrade to user scope.
	a := &App{}
	ctx := ctxClaims(&bauth.Claims{UserID: "u1", OrgID: "o1"})
	_, _, err := a.resolveScope(ctx, "org", false)
	if !errors.Is(err, ErrOrgScopeUnavailable) {
		t.Fatalf("want ErrOrgScopeUnavailable, got %v", err)
	}
}

func TestResolveScope_OrgWithoutOrgClaim(t *testing.T) {
	a := (&App{}).WithAuthz(&stubAuthz{allow: true})
	ctx := ctxClaims(&bauth.Claims{UserID: "u1"}) // no OrgID
	if _, _, err := a.resolveScope(ctx, "org", false); err == nil {
		t.Fatal("want error for org scope without org claim")
	}
}

func TestResolveScope_OrgRead_MemberAllowed(t *testing.T) {
	st := &stubAuthz{allow: true}
	a := (&App{}).WithAuthz(st)
	ctx := ctxClaims(&bauth.Claims{UserID: "u1", OrgID: "o1"})
	scope, id, err := a.resolveScope(ctx, "org", false)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "org" || id != "o1" {
		t.Fatalf("got (%s,%s)", scope, id)
	}
	if st.calls != 1 || st.lastWrite {
		t.Fatalf("authz calls=%d write=%v", st.calls, st.lastWrite)
	}
}

func TestResolveScope_OrgWrite_Denied(t *testing.T) {
	// Member (authz denies write) attempting an org write → forbidden,
	// no scope leaked.
	st := &stubAuthz{allow: false}
	a := (&App{}).WithAuthz(st)
	ctx := ctxClaims(&bauth.Claims{UserID: "u1", OrgID: "o1"})
	_, _, err := a.resolveScope(ctx, "org", true)
	if !errors.Is(err, ErrOrgForbidden) {
		t.Fatalf("want ErrOrgForbidden, got %v", err)
	}
	if !st.lastWrite {
		t.Fatal("expected write=true passed to authz")
	}
}

func TestResolveScope_UnknownScope(t *testing.T) {
	a := (&App{}).WithAuthz(&stubAuthz{allow: true})
	ctx := ctxClaims(&bauth.Claims{UserID: "u1", OrgID: "o1"})
	if _, _, err := a.resolveScope(ctx, "team", false); err == nil {
		t.Fatal("want error for unknown scope")
	}
}

func TestScopeOf(t *testing.T) {
	cases := map[string]string{
		``:                       "",
		`{}`:                     "",
		`{"scope":"org"}`:        "org",
		`{"scope":"user","x":1}`: "user",
		`not json`:               "",
	}
	for in, want := range cases {
		if got := scopeOf([]byte(in)); got != want {
			t.Errorf("scopeOf(%q)=%q want %q", in, got, want)
		}
	}
}
