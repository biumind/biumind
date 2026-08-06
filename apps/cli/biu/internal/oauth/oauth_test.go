package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPKCEPair(t *testing.T) {
	v, c, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if v == "" || c == "" {
		t.Fatal("empty pkce parts")
	}
	// SHA-256(verifier) base64url == challenge.
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if want != c {
		t.Errorf("challenge derivation wrong:\n got %s\nwant %s", c, want)
	}
}

func TestRandomStateNonce(t *testing.T) {
	a, _ := RandomState()
	b, _ := RandomState()
	if a == "" || a == b {
		t.Errorf("RandomState should return distinct non-empty values: %q vs %q", a, b)
	}
}

func TestStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	tokens := Tokens{
		AccessToken: "at-1", RefreshToken: "rt-1",
		TokenType: "Bearer",
		ExpiresAt: time.Now().Add(time.Hour).UTC().Round(time.Second),
	}
	if err := s.Save(tokens); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != tokens.AccessToken || got.RefreshToken != tokens.RefreshToken {
		t.Errorf("roundtrip lost data: %+v vs %+v", got, tokens)
	}
}

func TestStoreLoadMissing(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "missing.json"))
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "" {
		t.Errorf("missing file should yield zero-value tokens; got %+v", got)
	}
}

func TestExpiredFlag(t *testing.T) {
	if (Tokens{}).Expired() {
		t.Errorf("zero ExpiresAt should not be marked expired")
	}
	past := Tokens{ExpiresAt: time.Now().Add(-time.Minute)}
	if !past.Expired() {
		t.Errorf("past expiry should be expired")
	}
	near := Tokens{ExpiresAt: time.Now().Add(5 * time.Second)}
	if !near.Expired() {
		t.Errorf("within-leeway expiry should be expired")
	}
	future := Tokens{ExpiresAt: time.Now().Add(time.Hour)}
	if future.Expired() {
		t.Errorf("hour-out should not be expired")
	}
}

func TestBuildAuthURL(t *testing.T) {
	cfg := Config{
		AuthorizeURL: "https://idp.example/authorize",
		ClientID:     "client-x", Scopes: []string{"a", "b"},
	}
	got := buildAuthURL(cfg, "challenge-x", "state-x", 8088, false)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != "client-x" || q.Get("response_type") != "code" ||
		q.Get("code_challenge") != "challenge-x" ||
		q.Get("code_challenge_method") != "S256" ||
		q.Get("scope") != "a b" ||
		q.Get("redirect_uri") != "http://localhost:8088/callback" {
		t.Errorf("authurl wrong: %s", got)
	}
}

func TestBuildAuthURLManualRedirect(t *testing.T) {
	cfg := Config{
		AuthorizeURL: "https://idp.example/authorize",
		ClientID:     "x",
		ManualRedirectURL: "https://idp.example/oob",
	}
	got := buildAuthURL(cfg, "c", "s", 0, true)
	if !strings.Contains(got, "redirect_uri=https") {
		t.Errorf("manual redirect not used: %s", got)
	}
}

// fakeIdp simulates the /token endpoint for the manual flow + refresh.
func fakeIdp(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") == "" || r.Form.Get("code_verifier") == "" {
				http.Error(w, "missing fields", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "at-" + r.Form.Get("code"),
				"refresh_token": "rt-1",
				"token_type":    "Bearer",
				"expires_in":    60,
				"scope":         "openid",
			})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "rt-1" {
				http.Error(w, "bad refresh", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-refreshed",
				"token_type":   "Bearer",
				"expires_in":   60,
				// note: no rotation
			})
		default:
			http.Error(w, "unknown grant", 400)
		}
	}))
}

func TestManualLoginFlow(t *testing.T) {
	srv := fakeIdp(t)
	defer srv.Close()
	verifier, _, _ := GeneratePKCE()
	l := Login{
		Config: Config{
			AuthorizeURL: srv.URL + "/auth",
			TokenURL:     srv.URL,
			ClientID:     "test",
		},
		ManualCode:     "ABC123",
		ManualVerifier: verifier,
	}
	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tokens.AccessToken != "at-ABC123" {
		t.Errorf("access token wrong: %q", res.Tokens.AccessToken)
	}
	if res.Tokens.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt not populated")
	}
}

func TestRefreshPreservesRefreshTokenWhenServerOmits(t *testing.T) {
	srv := fakeIdp(t)
	defer srv.Close()
	l := Login{Config: Config{
		AuthorizeURL: srv.URL + "/auth",
		TokenURL:     srv.URL,
		ClientID:     "test",
	}}
	got, err := l.Refresh(context.Background(), Tokens{
		RefreshToken: "rt-1", AccessToken: "old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at-refreshed" {
		t.Errorf("access not rotated: %q", got.AccessToken)
	}
	if got.RefreshToken != "rt-1" {
		t.Errorf("refresh should be preserved when server omits: %q", got.RefreshToken)
	}
}

func TestRefreshErrorsWithoutToken(t *testing.T) {
	l := Login{Config: Config{
		AuthorizeURL: "x", TokenURL: "y", ClientID: "z",
	}}
	if _, err := l.Refresh(context.Background(), Tokens{}); err == nil {
		t.Errorf("missing refresh_token must error")
	}
}
