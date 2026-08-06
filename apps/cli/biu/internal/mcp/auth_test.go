package mcp

import (
	"net/http"
	"testing"
)

func TestParseAuthChallenge(t *testing.T) {
	tests := []struct {
		name             string
		header           string
		wantOK           bool
		wantRealm        string
		wantResource     string
		wantScope        string
	}{
		{
			name:   "no header",
			header: "",
			wantOK: false,
		},
		{
			name:   "non-bearer scheme",
			header: `Basic realm="api"`,
			wantOK: false,
		},
		{
			name:         "bearer with realm + resource_metadata",
			header:       `Bearer realm="example", resource_metadata="https://auth.example.com/.well-known/oauth-protected-resource", scope="read write"`,
			wantOK:       true,
			wantRealm:    "example",
			wantResource: "https://auth.example.com/.well-known/oauth-protected-resource",
			wantScope:    "read write",
		},
		{
			name:         "bearer with comma inside quoted value",
			header:       `Bearer realm="ex,ample", resource_metadata="https://x/y"`,
			wantOK:       true,
			wantRealm:    "ex,ample",
			wantResource: "https://x/y",
		},
		{
			name:         "bearer with unquoted value",
			header:       `Bearer realm=example, resource_metadata=https://x/y`,
			wantOK:       true,
			wantRealm:    "example",
			wantResource: "https://x/y",
		},
		{
			name:   "bearer with no params",
			header: `Bearer`,
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tt.header != "" {
				resp.Header.Set("WWW-Authenticate", tt.header)
			}
			got, ok := parseAuthChallenge(resp)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Realm != tt.wantRealm {
				t.Errorf("Realm=%q want %q", got.Realm, tt.wantRealm)
			}
			if got.ResourceMetadata != tt.wantResource {
				t.Errorf("ResourceMetadata=%q want %q", got.ResourceMetadata, tt.wantResource)
			}
			if got.Scope != tt.wantScope {
				t.Errorf("Scope=%q want %q", got.Scope, tt.wantScope)
			}
			if got.Raw != tt.header {
				t.Errorf("Raw=%q want %q", got.Raw, tt.header)
			}
		})
	}
}

func TestAuthState(t *testing.T) {
	a := &authState{}
	if a.NeedsAuth() {
		t.Fatal("fresh authState should not need auth")
	}
	a.SetChallenge(OAuthChallenge{ResourceMetadata: "https://x"})
	if !a.NeedsAuth() {
		t.Fatal("after SetChallenge should need auth")
	}
	a.SetTokens(OAuthTokens{AccessToken: "tk"})
	if a.NeedsAuth() {
		t.Fatal("after SetTokens should NOT need auth")
	}
	if got := a.Tokens(); got == nil || got.AccessToken != "tk" {
		t.Errorf("Tokens() = %+v", got)
	}
}

func TestOAuthChallenge_HasFlow(t *testing.T) {
	if (OAuthChallenge{}).HasFlow() {
		t.Error("empty challenge should not advertise a flow")
	}
	if !(OAuthChallenge{ResourceMetadata: "https://x"}).HasFlow() {
		t.Error("challenge with resource_metadata should advertise a flow")
	}
}
