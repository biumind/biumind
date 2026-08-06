package permissions

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse_Bare(t *testing.T) {
	p, err := Parse("hub.invoke")
	if err != nil {
		t.Fatal(err)
	}
	if p.Scope != "hub.invoke" || len(p.Params) != 0 {
		t.Errorf("got %+v", p)
	}
	if p.String() != "hub.invoke" {
		t.Errorf("roundtrip = %q", p.String())
	}
}

func TestParse_SingleParam(t *testing.T) {
	p, err := Parse("oauth:gmail")
	if err != nil {
		t.Fatal(err)
	}
	if p.Scope != "oauth" || !reflect.DeepEqual(p.Params, []string{"gmail"}) {
		t.Errorf("got %+v", p)
	}
	if p.String() != "oauth:gmail" {
		t.Errorf("roundtrip = %q", p.String())
	}
}

func TestParse_MultiParam(t *testing.T) {
	p, err := Parse("net.outbound:*.a.com,*.b.com,api.c.com")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Params, []string{"*.a.com", "*.b.com", "api.c.com"}) {
		t.Errorf("got %+v", p)
	}
}

func TestParse_TrimsWhitespaceInParams(t *testing.T) {
	p, err := Parse("net.outbound: *.a.com , *.b.com")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Params, []string{"*.a.com", "*.b.com"}) {
		t.Errorf("got %+v", p)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct{ in, msg string }{
		{"", "empty"},
		{":foo", "missing scope"},
		{"oauth:", "empty tail"},
		{"net.outbound:,foo", "empty param"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, err := Parse(c.in)
			if err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
			if !strings.Contains(err.Error(), c.msg) {
				t.Errorf("error = %v; want substring %q", err, c.msg)
			}
		})
	}
}

func TestParseAll_AggregatesErrors(t *testing.T) {
	_, err := ParseAll([]string{"hub.invoke", ":bad", "oauth:"})
	if err == nil {
		t.Fatal("expected aggregated errors")
	}
	// Both bad entries should be mentioned.
	if !strings.Contains(err.Error(), "[1]") || !strings.Contains(err.Error(), "[2]") {
		t.Errorf("error doesn't mention both indices: %v", err)
	}
}

func TestAppAttributes_OnlyGrantedPermissionsAppear(t *testing.T) {
	manifest, _ := ParseAll([]string{
		"net.outbound:*.feed.com",
		"hub.invoke",
		"notify.send", // optional, not granted
	})
	granted := []string{"net.outbound:*.feed.com", "hub.invoke"}

	attrs, err := AppAttributes(Installation{
		ID: "i-1", Identifier: "rss", AppID: "app_x",
		Scope: "user", ScopeID: "u-1",
		Enabled: true, Source: "marketplace", Version: "0.2.0",
	}, manifest, granted)
	if err != nil {
		t.Fatal(err)
	}

	scopes, _ := attrs["permissions"].([]string)
	wantScopes := map[string]bool{"net.outbound": true, "hub.invoke": true}
	for _, s := range scopes {
		if !wantScopes[s] {
			t.Errorf("unexpected scope %q in granted set %v", s, scopes)
		}
		delete(wantScopes, s)
	}
	if len(wantScopes) > 0 {
		t.Errorf("missing scopes %v from granted set %v", wantScopes, scopes)
	}

	netOut, _ := attrs["net_outbound"].([]string)
	if !reflect.DeepEqual(netOut, []string{"*.feed.com"}) {
		t.Errorf("net_outbound = %v, want [*.feed.com]", netOut)
	}
	if attrs["forced"] != false {
		t.Errorf("forced = %v", attrs["forced"])
	}
	if attrs["enabled"] != true {
		t.Errorf("enabled = %v", attrs["enabled"])
	}
}

func TestAppAttributes_DeterministicOrder(t *testing.T) {
	manifest, _ := ParseAll([]string{
		"net.outbound:*.b.com",
		"net.outbound:*.a.com",
	})
	granted := []string{"net.outbound:*.b.com", "net.outbound:*.a.com"}
	attrs, _ := AppAttributes(Installation{ID: "i", Scope: "user", ScopeID: "u"},
		manifest, granted)

	netOut := attrs["net_outbound"].([]string)
	if !reflect.DeepEqual(netOut, []string{"*.a.com", "*.b.com"}) {
		t.Errorf("net_outbound not sorted: %v", netOut)
	}
}

func TestAppAttributes_OAuthAndSecretsBuckets(t *testing.T) {
	manifest, _ := ParseAll([]string{
		"oauth:gmail",
		"oauth:google_calendar",
		"secrets.read:openai_key",
	})
	granted := []string{"oauth:gmail", "oauth:google_calendar", "secrets.read:openai_key"}
	attrs, _ := AppAttributes(Installation{ID: "i", Scope: "user", ScopeID: "u"},
		manifest, granted)

	if !reflect.DeepEqual(attrs["oauth_providers"], []string{"gmail", "google_calendar"}) {
		t.Errorf("oauth_providers = %v", attrs["oauth_providers"])
	}
	if !reflect.DeepEqual(attrs["secret_providers"], []string{"openai_key"}) {
		t.Errorf("secret_providers = %v", attrs["secret_providers"])
	}
}

func TestAllActions_Sorted(t *testing.T) {
	all := AllActions()
	for i := 1; i < len(all); i++ {
		if all[i-1] > all[i] {
			t.Errorf("not sorted at index %d: %v", i, all)
		}
	}
	if len(all) != 16 {
		t.Errorf("vocabulary size = %d, want 16 (update test if intentional)", len(all))
	}
}
