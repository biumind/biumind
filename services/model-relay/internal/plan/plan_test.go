package plan

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

func TestBucketFor(t *testing.T) {
	if got := BucketFor("hub.rpm", PlanPro); got != "hub.rpm.pro" {
		t.Errorf("got %q", got)
	}
	if got := BucketFor("hub.tpm", ""); got != "hub.tpm.free" {
		t.Errorf("empty plan should fall through to free, got %q", got)
	}
}

func TestSpecsFor_CoversEveryPlan(t *testing.T) {
	specs := SpecsFor(DefaultLimits)
	for p := range DefaultLimits {
		if _, ok := specs[BucketFor("hub.rpm", p)]; !ok {
			t.Errorf("missing rpm spec for %s", p)
		}
		if _, ok := specs[BucketFor("hub.tpm", p)]; !ok {
			t.Errorf("missing tpm spec for %s", p)
		}
	}
}

func TestSpecsFor_LimitsMonotonic(t *testing.T) {
	specs := SpecsFor(DefaultLimits)
	if specs[BucketFor("hub.rpm", PlanPro)].Limit <=
		specs[BucketFor("hub.rpm", PlanFree)].Limit {
		t.Errorf("pro RPM should exceed free")
	}
	if specs[BucketFor("hub.tpm", PlanTeam)].Limit <=
		specs[BucketFor("hub.tpm", PlanPro)].Limit {
		t.Errorf("team TPM should exceed pro")
	}
}

func TestPlanFromRequest_NilResolverFallsBackToFree(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := PlanFromRequest(r, nil); got != PlanFree {
		t.Errorf("nil resolver: got %s", got)
	}
}

func TestPlanFromRequest_NoClaimsFallsBackToFree(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	resolver := StaticResolver(PlanTeam) // would return team, but no claims
	if got := PlanFromRequest(r, resolver); got != PlanFree {
		t.Errorf("no claims: got %s", got)
	}
}

func TestPlanFromRequest_HappyPath(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(),
		&bauth.Claims{UserID: "user_1"}))
	got := PlanFromRequest(r, ResolverFunc(
		func(_ context.Context, uid string) (Plan, error) {
			if uid != "user_1" {
				t.Fatalf("uid: %s", uid)
			}
			return PlanPro, nil
		}))
	if got != PlanPro {
		t.Errorf("expected pro, got %s", got)
	}
}

func TestPlanFromRequest_ResolverErrorFallsBackToFree(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(),
		&bauth.Claims{UserID: "u"}))
	got := PlanFromRequest(r, ResolverFunc(
		func(context.Context, string) (Plan, error) {
			return "", errors.New("identity unreachable")
		}))
	if got != PlanFree {
		t.Errorf("error path: got %s", got)
	}
}

func TestPlanFromRequest_UnknownPlanFallsBackToFree(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(),
		&bauth.Claims{UserID: "u"}))
	got := PlanFromRequest(r, StaticResolver(Plan("enterprise-secret")))
	if got != PlanFree {
		t.Errorf("unknown plan: got %s", got)
	}
}
