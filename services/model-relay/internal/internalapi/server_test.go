// Tests for the internal credentials decrypt endpoint.
//
// Two layers of tests:
//
//  1. Auth / shape unit tests — drive the handler via httptest with a
//     nil Vault for 503 paths and a real Vault (DB-backed) for 200
//     paths. No mocking of Vault — it's a thin struct over Repo, easier
//     to use the real thing with a fresh envelope.
//
//  2. End-to-end with envelope round-trip: Save plaintext → call the
//     endpoint → assert returned api_key matches.
//
// DB tests skip when DATABASE_URL is unset, same convention as
// adminapi/server_test.go.

package internalapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/model-relay/internal/keys"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

const testToken = "test-internal-token-do-not-use-in-prod"

// ─── pure unit tests (no DB) ──────────────────────────────────────

func TestRequireToken(t *testing.T) {
	srv := &Server{Token: testToken}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cases := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"missing bearer", "", http.StatusUnauthorized},
		{"non-bearer prefix", "Token " + testToken, http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-secret", http.StatusUnauthorized},
		{"empty token after Bearer", "Bearer ", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(
				http.MethodPost,
				ts.URL+"/v1/internal/credentials/00000000-0000-0000-0000-000000000001/get-decrypted",
				nil,
			)
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, c.wantStatus)
			}
		})
	}
}

func TestVaultNotWired(t *testing.T) {
	srv := &Server{Token: testToken, Vault: nil}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(
		http.MethodPost,
		ts.URL+"/v1/internal/credentials/00000000-0000-0000-0000-000000000001/get-decrypted",
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when vault nil, got %d", resp.StatusCode)
	}
}

func TestEmptyTokenSkipsAuth(t *testing.T) {
	// In tests, Server with Token="" must allow any request through.
	// Production never runs this configuration.
	srv := &Server{Token: "", Vault: nil}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(
		http.MethodPost,
		ts.URL+"/v1/internal/credentials/00000000-0000-0000-0000-000000000001/get-decrypted",
		nil,
	)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	// Should reach handler (Vault nil → 503), not 401.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (vault nil), got %d (token unexpectedly enforced)",
			resp.StatusCode)
	}
}

func TestBadCredentialID(t *testing.T) {
	srv := &Server{Token: testToken, Vault: nil} // Vault not exercised — path parse fires first
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(
		http.MethodPost,
		ts.URL+"/v1/internal/credentials/not-a-uuid/get-decrypted",
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad uuid, got %d", resp.StatusCode)
	}
}

// ─── DB-backed integration test ───────────────────────────────────

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping DB integration test")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func freshEnvelope(t *testing.T) *keys.Envelope {
	t.Helper()
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("rand kek: %v", err)
	}
	env, err := keys.NewEnvelope(kek)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return env
}

// TestGetDecryptedRoundTrip: Save plaintext via Vault, call the endpoint,
// assert the returned api_key matches. This is the contract aigc relies on.
func TestGetDecryptedRoundTrip(t *testing.T) {
	pool := openDB(t)
	store := registry.NewStore(pool)
	ctx := context.Background()

	// Seed a provider for FK
	pcode := "p_internalapi_" + uuid.NewString()[:8]
	prov, err := store.Providers.Insert(ctx, registry.ProviderInput{
		Code: pcode, Name: "InternalAPI Test", Protocol: registry.ProtocolOpenAICompat,
	})
	if err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	vault := registry.NewCredentialVault(store.Credentials, freshEnvelope(t))

	const plaintext = "sk-test-1234567890abcdef"
	safe, err := vault.Save(ctx, registry.SaveInput{
		ProviderID:     prov.ID,
		Label:          "test-key",
		Plaintext:      plaintext,
		BaseURL:        "https://api.example.com",
		HeaderOverride: map[string]string{"X-Tenant": "biumind"},
	})
	if err != nil {
		t.Fatalf("vault save: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", safe.ID) //nolint:errcheck

	srv := &Server{Token: testToken, Vault: vault}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(
		http.MethodPost,
		ts.URL+"/v1/internal/credentials/"+safe.ID.String()+"/get-decrypted",
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}

	var out struct {
		APIKey  string            `json:"api_key"`
		BaseURL string            `json:"base_url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.APIKey != plaintext {
		t.Fatalf("api_key = %q, want %q", out.APIKey, plaintext)
	}
	if out.BaseURL != "https://api.example.com" {
		t.Fatalf("base_url = %q", out.BaseURL)
	}
	if out.Headers["X-Tenant"] != "biumind" {
		t.Fatalf("headers missing X-Tenant: %v", out.Headers)
	}
}

func TestGetDecryptedNotFound(t *testing.T) {
	pool := openDB(t)
	store := registry.NewStore(pool)
	vault := registry.NewCredentialVault(store.Credentials, freshEnvelope(t))

	srv := &Server{Token: testToken, Vault: vault}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(
		http.MethodPost,
		ts.URL+"/v1/internal/credentials/"+uuid.New().String()+"/get-decrypted",
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
