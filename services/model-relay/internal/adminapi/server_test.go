// Integration tests for the admin HTTP surface. They run a fresh
// http.ServeMux with the full Server stack mounted, then drive it via
// httptest.Server. Skip when DATABASE_URL is unset.
//
// Auth: every test acquires a token via a local HS256 Signer and the
// adminapi RoleCache reads identity.role_permissions from the same dev
// DB that already has 00016_model_relay_perms.sql applied (or applies
// it inline if the dev fixture is short).
//
// Test scope: positive path (200/201/204) for each endpoint, plus
// negative gates (401/403/409). Full functional coverage of the
// underlying registry / vault / probe is in their respective package
// tests; this file verifies the HTTP layer doesn't drop fields,
// permissions are enforced, and the JSON shape matches the contract.

package adminapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"

	"github.com/biumind/biumind/services/model-relay/internal/health"
	"github.com/biumind/biumind/services/model-relay/internal/keys"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
	"github.com/biumind/biumind/services/model-relay/internal/router"
)

const (
	testJWTSecret = "test-secret-do-not-use-in-prod"
	testIssuer    = "test-iss"
	testAudience  = "test-aud"
)

// ensureRBACSchema creates the identity.{roles,permissions,role_permissions}
// tables if absent — dev biu-postgres has an old identity image that
// hasn't run 00003_rbac.sql, so tests bring up the minimum schema
// they need. Idempotent.
func ensureRBACSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS identity;
		CREATE TABLE IF NOT EXISTS identity.roles (
			name text PRIMARY KEY,
			display_name text NOT NULL,
			description text NOT NULL DEFAULT '',
			is_system boolean NOT NULL DEFAULT false,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TABLE IF NOT EXISTS identity.permissions (
			name text PRIMARY KEY,
			resource text NOT NULL,
			action text NOT NULL,
			scope text,
			description text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TABLE IF NOT EXISTS identity.role_permissions (
			role_name text NOT NULL,
			permission_name text NOT NULL,
			granted_at timestamptz NOT NULL DEFAULT now(),
			granted_by uuid,
			PRIMARY KEY (role_name, permission_name)
		);
	`)
	if err != nil {
		t.Fatalf("rbac schema: %v", err)
	}
}

// rbacFixture inserts a single role with the given permissions if not
// already present. Idempotent — designed for tests against the shared
// dev DB.
func ensureTestRole(t *testing.T, pool *pgxpool.Pool, role string, perms []string) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx,
		`INSERT INTO identity.roles (name, display_name, description, is_system)
		 VALUES ($1, $1, 'test role', false)
		 ON CONFLICT (name) DO NOTHING`, role)
	for _, p := range perms {
		_, _ = pool.Exec(ctx,
			`INSERT INTO identity.role_permissions (role_name, permission_name)
			 VALUES ($1, $2) ON CONFLICT DO NOTHING`, role, p)
	}
}

func ensureModelRelayPerms(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, p := range []struct{ name, resource, action, scope string }{
		{"models:read", "models", "read", ""},
		{"models:write", "models", "write", ""},
		{"model_credentials:read", "model_credentials", "read", "safe"},
		{"model_credentials:write", "model_credentials", "write", ""},
		{"pricing:write", "pricing", "write", ""},
		{"fx_rates:write", "fx_rates", "write", ""},
	} {
		_, _ = pool.Exec(ctx,
			`INSERT INTO identity.permissions (name, resource, action, scope, description)
			 VALUES ($1, $2, $3, NULLIF($4, ''), 'test') ON CONFLICT DO NOTHING`,
			p.name, p.resource, p.action, p.scope)
	}
}

// adminFixture wires Server + httptest server and returns a per-test
// token signer. Cleanup deletes ad-hoc rows but leaves seed data alone.
type adminFixture struct {
	pool   *pgxpool.Pool
	store  *registry.Store
	server *Server
	srv    *httptest.Server
	signer *bauth.Signer
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgx: %v", err)
	}
	t.Cleanup(pool.Close)

	// Make sure permissions + roles needed by the tests exist.
	ensureRBACSchema(t, pool)
	ensureModelRelayPerms(t, pool)
	ensureTestRole(t, pool, "admin", []string{
		"models:read", "models:write",
		"model_credentials:read", "model_credentials:write",
		"pricing:write", "fx_rates:write",
	})
	ensureTestRole(t, pool, "viewer", []string{"models:read"})

	store := registry.NewStore(pool)
	kek := make([]byte, 32)
	_, _ = rand.Read(kek)
	env, _ := keys.NewEnvelope(kek)
	vault := registry.NewCredentialVault(store.Credentials, env)

	cache := registry.NewCache(store, registry.CacheConfig{TTL: 1 * time.Minute})
	if err := cache.Start(context.Background()); err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(cache.Close)

	adaptors := provider.NewRegistry()
	adaptors.Register(openai.New())
	probe := health.New(health.Config{
		Store: store, Vault: vault, Adaptors: adaptors,
		Timeout: 1 * time.Second,
	})

	stratReg := router.NewRegistry()
	stratReg.Register(router.NewWeighted())

	roleCache := bauth.NewRoleCache(pool)
	if err := roleCache.Reload(context.Background()); err != nil {
		t.Fatalf("role cache reload: %v", err)
	}
	verifier := bauth.NewVerifier(testJWTSecret, testIssuer, testAudience)
	signer := bauth.NewSigner(testJWTSecret, testIssuer, testAudience, 1*time.Hour)

	srv := &Server{
		Store: store, Vault: vault, Cache: cache,
		Probe: probe,
		Strategies:  stratReg,
		RoleCache:   roleCache,
		JWTVerifier: verifier,
	}
	mux := http.NewServeMux()
	srv.Mount(mux)

	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)

	return &adminFixture{
		pool: pool, store: store, server: srv, srv: httpSrv, signer: signer,
	}
}

func (fx *adminFixture) token(t *testing.T, role string) string {
	t.Helper()
	tok, err := fx.signer.Sign(&bauth.Claims{
		UserID: uuid.New().String(),
		Roles:  []string{role},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func (fx *adminFixture) do(t *testing.T, method, path, role string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, fx.srv.URL+path, rdr)
	req.Header.Set("Authorization", "Bearer "+fx.token(t, role))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func decodeBody(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
}

// ─── Tests ────────────────────────────────────────────────────────

func TestAdmin_AuthGate(t *testing.T) {
	fx := newAdminFixture(t)

	// No token → 401
	resp, _ := http.Get(fx.srv.URL + "/v1/admin/providers")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// viewer role can list providers (read perm), but cannot create (write perm)
	resp, _ = fx.do(t, "GET", "/v1/admin/providers", "viewer", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer list: %d", resp.StatusCode)
	}

	resp, _ = fx.do(t, "POST", "/v1/admin/providers", "viewer", map[string]any{
		"code": "deny", "name": "x", "protocol": "openai_compat",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create: expected 403, got %d", resp.StatusCode)
	}
}

func TestAdmin_ProviderCRUD(t *testing.T) {
	fx := newAdminFixture(t)

	code := fmt.Sprintf("p_admin_%d", time.Now().UnixNano())
	resp, body := fx.do(t, "POST", "/v1/admin/providers", "admin", map[string]any{
		"code": code, "name": "Acme", "protocol": "openai_compat",
		"description": "test",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d body=%s", resp.StatusCode, body)
	}
	var p registry.Provider
	decodeBody(t, body, &p)
	if p.ID == uuid.Nil || p.Code != code {
		t.Fatalf("created provider missing fields: %+v", p)
	}
	defer fx.pool.Exec(context.Background(),
		"DELETE FROM model_relay.providers WHERE id=$1", p.ID) //nolint:errcheck

	// GET
	resp, body = fx.do(t, "GET", "/v1/admin/providers/"+p.ID.String(), "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d body=%s", resp.StatusCode, body)
	}

	// PATCH
	resp, _ = fx.do(t, "PATCH", "/v1/admin/providers/"+p.ID.String(), "admin", map[string]any{
		"code": code, "name": "Acme Renamed", "protocol": "openai_compat",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d", resp.StatusCode)
	}

	// LIST should include it
	resp, body = fx.do(t, "GET", "/v1/admin/providers?q="+code, "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var list struct {
		Items []registry.Provider `json:"items"`
		Total int                 `json:"total"`
	}
	decodeBody(t, body, &list)
	if list.Total < 1 {
		t.Fatalf("list filter %q returned 0", code)
	}

	// DELETE
	resp, _ = fx.do(t, "DELETE", "/v1/admin/providers/"+p.ID.String(), "admin", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
}

func TestAdmin_DuplicateProviderConflict(t *testing.T) {
	fx := newAdminFixture(t)
	code := fmt.Sprintf("p_dup_%d", time.Now().UnixNano())

	resp, body := fx.do(t, "POST", "/v1/admin/providers", "admin", map[string]any{
		"code": code, "name": "x", "protocol": "openai_compat",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first: %d body=%s", resp.StatusCode, body)
	}
	var p registry.Provider
	decodeBody(t, body, &p)
	defer fx.pool.Exec(context.Background(),
		"DELETE FROM model_relay.providers WHERE id=$1", p.ID) //nolint:errcheck

	resp, _ = fx.do(t, "POST", "/v1/admin/providers", "admin", map[string]any{
		"code": code, "name": "y", "protocol": "openai_compat",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestAdmin_CredentialFullLifecycle(t *testing.T) {
	fx := newAdminFixture(t)

	// Seed provider
	pcode := fmt.Sprintf("p_cred_%d", time.Now().UnixNano())
	prov, _ := fx.store.Providers.Insert(context.Background(), registry.ProviderInput{
		Code: pcode, Name: "P", Protocol: registry.ProtocolOpenAICompat,
	})
	defer fx.pool.Exec(context.Background(),
		"DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	// CREATE credential — must encrypt; response must NOT carry plaintext.
	resp, body := fx.do(t, "POST", "/v1/admin/credentials", "admin", map[string]any{
		"provider_id": prov.ID.String(),
		"label":       "Acme",
		"plaintext":   "sk-acme-1234567890abcdef",
		"base_url":    "https://api.example.com",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d body=%s", resp.StatusCode, body)
	}
	if bytes.Contains(body, []byte("sk-acme-1234567890abcdef")) {
		t.Fatalf("plaintext leaked in create response")
	}
	var safe registry.CredentialSafe
	decodeBody(t, body, &safe)
	defer fx.pool.Exec(context.Background(),
		"DELETE FROM model_relay.credentials WHERE id=$1", safe.ID) //nolint:errcheck

	if safe.KeyPreview != "sk-ac...cdef" {
		t.Fatalf("preview unexpected: %q", safe.KeyPreview)
	}

	// LIST never includes envelope bytes
	resp, body = fx.do(t, "GET", "/v1/admin/credentials?provider_id="+prov.ID.String(), "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	if bytes.Contains(body, []byte("ciphertext")) {
		t.Fatalf("envelope key leaked in list JSON")
	}

	// PATCH with plaintext = rotation; response still scrubbed
	resp, body = fx.do(t, "PATCH", "/v1/admin/credentials/"+safe.ID.String(), "admin",
		map[string]any{
			"label":     "Acme Rotated",
			"base_url":  "https://api.example.com",
			"plaintext": "sk-rotated-key-zzzzz",
			"status":    "active",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate: %d body=%s", resp.StatusCode, body)
	}
	var rotated registry.CredentialSafe
	decodeBody(t, body, &rotated)
	if rotated.KeyPreview == safe.KeyPreview {
		t.Fatalf("preview unchanged after rotation")
	}

	// DELETE — no channels yet, should succeed
	resp, _ = fx.do(t, "DELETE", "/v1/admin/credentials/"+safe.ID.String(), "admin", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
}

func TestAdmin_ModelLifecycleWithGroupBinding(t *testing.T) {
	fx := newAdminFixture(t)

	mcode := fmt.Sprintf("m_admin_%d", time.Now().UnixNano())
	resp, body := fx.do(t, "POST", "/v1/admin/models", "admin", map[string]any{
		"code": mcode, "display_name": "M", "min_plan": "pro",
		"context_window": 8000, "status": "active",
		"capabilities":   map[string]any{"vision": true},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create model: %d body=%s", resp.StatusCode, body)
	}
	var m registry.Model
	decodeBody(t, body, &m)
	defer fx.pool.Exec(context.Background(),
		"DELETE FROM model_relay.models WHERE id=$1", m.ID) //nolint:errcheck

	if !m.ManualOverride {
		t.Fatalf("admin-created model should default to manual_override=true")
	}
	if !m.Capabilities.Vision {
		t.Fatalf("capabilities not roundtripped")
	}

	// GET — should include the auto-bound default group
	resp, body = fx.do(t, "GET", "/v1/admin/models/"+m.ID.String(), "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d body=%s", resp.StatusCode, body)
	}
	var detail struct {
		Model  registry.Model        `json:"model"`
		Groups []registry.ModelGroup `json:"groups"`
	}
	decodeBody(t, body, &detail)
	if len(detail.Groups) != 1 || detail.Groups[0].ID != registry.DefaultGroupID {
		t.Fatalf("expected auto-bound default group, got %+v", detail.Groups)
	}
}

func TestAdmin_PricingAppendOnly(t *testing.T) {
	fx := newAdminFixture(t)
	ctx := context.Background()

	mcode := fmt.Sprintf("m_pricing_%d", time.Now().UnixNano())
	m, _ := fx.store.Models.Insert(ctx, registry.ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: registry.PlanFree,
		Status: registry.StatusActive,
	})
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", m.ID) //nolint:errcheck

	// First set
	resp, _ := fx.do(t, "POST", "/v1/admin/pricing/"+m.ID.String(), "admin",
		map[string]any{"currency": "USD", "input_per_mtok": 1.5, "output_per_mtok": 4.5})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("set 1: %d", resp.StatusCode)
	}

	// Second set
	resp, _ = fx.do(t, "POST", "/v1/admin/pricing/"+m.ID.String(), "admin",
		map[string]any{"currency": "USD", "input_per_mtok": 2.0, "output_per_mtok": 6.0})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("set 2: %d", resp.StatusCode)
	}

	// GET current returns the latest
	resp, body := fx.do(t, "GET", "/v1/admin/pricing/"+m.ID.String(), "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d", resp.StatusCode)
	}
	var cur registry.Pricing
	decodeBody(t, body, &cur)
	if cur.InputPerMTok != 2.0 {
		t.Fatalf("expected latest pricing, got %+v", cur)
	}

	// History returns 2 rows
	resp, body = fx.do(t, "GET", "/v1/admin/pricing/"+m.ID.String()+"/history", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history: %d", resp.StatusCode)
	}
	var hist struct {
		Items []registry.Pricing `json:"items"`
		Total int                `json:"total"`
	}
	decodeBody(t, body, &hist)
	if hist.Total != 2 {
		t.Fatalf("expected 2 history rows, got %d", hist.Total)
	}
}

func TestAdmin_FxRatesUpsert(t *testing.T) {
	fx := newAdminFixture(t)

	resp, body := fx.do(t, "GET", "/v1/admin/fx-rates", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("USD")) {
		t.Fatalf("fx list missing seed: %s", body)
	}

	// Try setting USD→CNY to a different rate
	resp, _ = fx.do(t, "PUT", "/v1/admin/fx-rates", "admin", map[string]any{
		"from_currency": "USD", "to_currency": "CNY",
		"rate": 7.50, "source": "manual",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert: %d", resp.StatusCode)
	}

	// Read back
	resp, body = fx.do(t, "GET", "/v1/admin/fx-rates", "admin", nil)
	if !bytes.Contains(body, []byte("7.5")) {
		t.Fatalf("upserted rate not visible: %s", body)
	}

	// Restore seed value
	_, _ = fx.do(t, "PUT", "/v1/admin/fx-rates", "admin", map[string]any{
		"from_currency": "USD", "to_currency": "CNY",
		"rate": 7.20, "source": "manual",
	})
}

func TestAdmin_PathParamValidation(t *testing.T) {
	fx := newAdminFixture(t)
	resp, _ := fx.do(t, "GET", "/v1/admin/providers/not-a-uuid", "admin", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad uuid, got %d", resp.StatusCode)
	}
}

func TestAdmin_ChannelTestStampsResult(t *testing.T) {
	// Smoke test: real upstream is mocked via stub; we just want to
	// confirm the route reaches the probe and stamps the result.
	fx := newAdminFixture(t)
	ctx := context.Background()

	pcode := fmt.Sprintf("p_chtest_%d", time.Now().UnixNano())
	prov, _ := fx.store.Providers.Insert(ctx, registry.ProviderInput{
		Code: pcode, Name: "P", Protocol: registry.ProtocolOpenAICompat,
	})
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	cred, _ := fx.server.Vault.Save(ctx, registry.SaveInput{
		ProviderID: prov.ID, Label: "L", Plaintext: "sk-test-stub",
		BaseURL: "http://127.0.0.1:65535", // unreachable → probe will fail with network
	})
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", cred.ID) //nolint:errcheck

	mcode := fmt.Sprintf("m_chtest_%d", time.Now().UnixNano())
	mdl, _ := fx.store.Models.Insert(ctx, registry.ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: registry.PlanFree,
		Status: registry.StatusActive,
	})
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", mdl.ID) //nolint:errcheck

	ch, _ := fx.store.Channels.Insert(ctx, registry.ChannelInput{
		ModelID: mdl.ID, CredentialID: cred.ID, UpstreamModel: "x",
		Priority: 100, Weight: 1, Status: registry.StatusActive,
	})
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.channels WHERE id=$1", ch.ID) //nolint:errcheck

	resp, body := fx.do(t, "POST", "/v1/admin/channels/"+ch.ID.String()+"/test", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test channel: %d body=%s", resp.StatusCode, body)
	}
	var res health.ProbeResult
	decodeBody(t, body, &res)
	if res.OK {
		t.Fatalf("unreachable upstream should fail: %+v", res)
	}
}

// Smoke that the JSON encoder doesn't include any envelope bytes for
// any credential read path. Exhaustive belt-and-braces.
func TestAdmin_CredentialNeverLeaksEnvelope(t *testing.T) {
	fx := newAdminFixture(t)
	ctx := context.Background()

	pcode := fmt.Sprintf("p_leak_%d", time.Now().UnixNano())
	prov, _ := fx.store.Providers.Insert(ctx, registry.ProviderInput{
		Code: pcode, Name: "P", Protocol: registry.ProtocolOpenAICompat,
	})
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	cred, err := fx.server.Vault.Save(ctx, registry.SaveInput{
		ProviderID: prov.ID, Label: "L", Plaintext: "sk-leak-test-key",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", cred.ID) //nolint:errcheck

	for _, path := range []string{
		"/v1/admin/credentials",
		"/v1/admin/credentials/" + cred.ID.String(),
	} {
		resp, body := fx.do(t, "GET", path, "admin", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d body=%s", path, resp.StatusCode, body)
		}
		// Envelope fields must not appear under any name
		for _, forbidden := range []string{"ciphertext", "wrapped_dek", `"iv":`, "wrap_iv"} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Fatalf("%s response contains %q: %s", path, forbidden, body)
			}
		}
	}
}

var _ = errors.New // keep imports tidy
