package engine

import "testing"

const samplePolicies = `
@id("allow_owner_read")
permit(
    principal,
    action == Action::"wiki:Page::read",
    resource is Page
)
when { resource.owner == principal.id };

@id("allow_public_read")
permit(
    principal,
    action == Action::"wiki:Page::read",
    resource is Page
)
when { resource.share_mode == "public_read" };

@id("forbid_quota")
forbid(
    principal,
    action == Action::"model-relay:Model::use",
    resource
)
when { principal.spent_today >= principal.daily_limit };

@id("allow_model_use")
permit(
    principal,
    action == Action::"model-relay:Model::use",
    resource is Model
);
`

func newEngine(t *testing.T) *Engine {
	t.Helper()
	e := New()
	if err := e.LoadPolicies([]byte(samplePolicies)); err != nil {
		t.Fatalf("load: %v", err)
	}
	return e
}

func TestOwnerCanReadOwnPage(t *testing.T) {
	e := newEngine(t)
	res, err := e.Check(Input{
		Principal: Entity{Type: "User", ID: "u-1", Attributes: map[string]any{"id": "u-1"}},
		Action:    "wiki:Page::read",
		Resource: Entity{Type: "Page", ID: "p-1", Attributes: map[string]any{
			"owner":      "u-1",
			"share_mode": "private",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionAllow {
		t.Errorf("got %s, errors=%v", res.Decision, res.Errors)
	}
}

func TestStrangerDeniedPrivatePage(t *testing.T) {
	e := newEngine(t)
	res, err := e.Check(Input{
		Principal: Entity{Type: "User", ID: "u-2", Attributes: map[string]any{"id": "u-2"}},
		Action:    "wiki:Page::read",
		Resource: Entity{Type: "Page", ID: "p-1", Attributes: map[string]any{
			"owner":      "u-1",
			"share_mode": "private",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionDeny {
		t.Errorf("got %s; want DENY", res.Decision)
	}
}

func TestPublicPageReadable(t *testing.T) {
	e := newEngine(t)
	res, err := e.Check(Input{
		Principal: Entity{Type: "User", ID: "u-2", Attributes: map[string]any{"id": "u-2"}},
		Action:    "wiki:Page::read",
		Resource: Entity{Type: "Page", ID: "p-2", Attributes: map[string]any{
			"owner":      "u-1",
			"share_mode": "public_read",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionAllow {
		t.Errorf("got %s, errors=%v", res.Decision, res.Errors)
	}
}

func TestQuotaForbid(t *testing.T) {
	e := newEngine(t)
	res, err := e.Check(Input{
		Principal: Entity{Type: "User", ID: "u-1", Attributes: map[string]any{
			"spent_today": int64(150),
			"daily_limit": int64(100),
		}},
		Action:   "model-relay:Model::use",
		Resource: Entity{Type: "Model", ID: "claude-sonnet"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionDeny {
		t.Errorf("expected DENY (over quota); got %s", res.Decision)
	}
}

func TestQuotaUnderLimitAllowed(t *testing.T) {
	e := newEngine(t)
	res, err := e.Check(Input{
		Principal: Entity{Type: "User", ID: "u-1", Attributes: map[string]any{
			"spent_today": int64(50),
			"daily_limit": int64(100),
		}},
		Action:   "model-relay:Model::use",
		Resource: Entity{Type: "Model", ID: "claude-sonnet"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionAllow {
		t.Errorf("expected ALLOW (under quota); got %s", res.Decision)
	}
}

func TestUnknownActionDenied(t *testing.T) {
	e := newEngine(t)
	res, err := e.Check(Input{
		Principal: Entity{Type: "User", ID: "u-1"},
		Action:    "unknown:Foo::do",
		Resource:  Entity{Type: "Foo", ID: "f-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionDeny {
		t.Errorf("default-deny broken; got %s", res.Decision)
	}
}

func TestPolicyCount(t *testing.T) {
	e := newEngine(t)
	if got := e.PolicyCount(); got != 4 {
		t.Errorf("PolicyCount = %d; want 4", got)
	}
}

func TestBadPolicyFails(t *testing.T) {
	e := New()
	if err := e.LoadPolicies([]byte("this is not cedar")); err == nil {
		t.Fatal("expected parse error")
	}
}
