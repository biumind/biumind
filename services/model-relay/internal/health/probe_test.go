package health

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/model-relay/internal/keys"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
)

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping health probe integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// Build a minimal OpenAI-compatible upstream stub. The handler dispatches
// based on path, returning either a 200 chat-completions or the
// configured error.
func newStubUpstream(t *testing.T, behavior string) *httptest.Server {
	t.Helper()
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth check — every path that matters needs the bearer
		auth := r.Header.Get("Authorization")
		if auth == "" || auth == "Bearer " {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch behavior {
		case "ok":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmpl-test",
				"model":  "gpt-4o-mini",
				"object": "chat.completion",
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "hello back",
					},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{
					"prompt_tokens":     5,
					"completion_tokens": 3,
					"total_tokens":      8,
				},
			})
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream busted"}}`))
		case "garbage":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html>not json</html>`))
		case "slow":
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(s.Close)
	return s
}

type probeFixture struct {
	pool     *pgxpool.Pool
	store    *registry.Store
	vault    *registry.CredentialVault
	probe    *Probe
	provider *registry.Provider
	cred     *registry.CredentialSafe
	model    *registry.Model
	channel  *registry.Channel
	stub     *httptest.Server
}

func newProbeFixture(t *testing.T, behavior string) *probeFixture {
	t.Helper()
	pool := openDB(t)
	store := registry.NewStore(pool)
	ctx := context.Background()

	kek := make([]byte, 32)
	_, _ = rand.Read(kek)
	env, _ := keys.NewEnvelope(kek)
	vault := registry.NewCredentialVault(store.Credentials, env)

	stub := newStubUpstream(t, behavior)

	pcode := fmt.Sprintf("p_health_%d", time.Now().UnixNano())
	prov, err := store.Providers.Insert(ctx, registry.ProviderInput{
		Code: pcode, Name: "Stub", Protocol: registry.ProtocolOpenAICompat,
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck
	})

	safe, err := vault.Save(ctx, registry.SaveInput{
		ProviderID: prov.ID, Label: "L", Plaintext: "sk-stub-test-key",
		BaseURL: stub.URL, // adaptor uses BaseURL to find upstream
	})
	if err != nil {
		t.Fatalf("save credential: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", safe.ID) //nolint:errcheck
	})

	mcode := fmt.Sprintf("m_health_%d", time.Now().UnixNano())
	mdl, err := store.Models.Insert(ctx, registry.ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: registry.PlanFree,
		Status: registry.StatusActive,
	})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", mdl.ID) //nolint:errcheck
	})

	ch, err := store.Channels.Insert(ctx, registry.ChannelInput{
		ModelID: mdl.ID, CredentialID: safe.ID,
		UpstreamModel: "gpt-4o-mini",
		Priority:      100, Weight: 1, Status: registry.StatusActive,
	})
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM model_relay.channels WHERE id=$1", ch.ID) //nolint:errcheck
	})

	adaptors := provider.NewRegistry()
	adaptors.Register(openai.New())

	p := New(Config{
		Store:    store,
		Vault:    vault,
		Adaptors: adaptors,
		Timeout:  1 * time.Second,
	})

	return &probeFixture{
		pool: pool, store: store, vault: vault, probe: p,
		provider: prov, cred: safe, model: mdl, channel: ch, stub: stub,
	}
}

func TestProbeRunChannel_OK(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	res := fx.probe.RunChannel(context.Background(), fx.channel.ID)
	if !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: %d", res.StatusCode)
	}
	if res.Tokens != 8 {
		t.Fatalf("tokens: %d", res.Tokens)
	}
	if res.LatencyMs <= 0 {
		t.Fatalf("latency unset")
	}
}

func TestProbeRunChannel_Unauthorized(t *testing.T) {
	fx := newProbeFixture(t, "401")
	res := fx.probe.RunChannel(context.Background(), fx.channel.ID)
	if res.OK {
		t.Fatalf("should fail")
	}
	if res.ErrorCode != CodeUnauthorized {
		t.Fatalf("expected unauthorized, got %s (%s)", res.ErrorCode, res.Error)
	}
}

func TestProbeRunChannel_RateLimited(t *testing.T) {
	fx := newProbeFixture(t, "429")
	res := fx.probe.RunChannel(context.Background(), fx.channel.ID)
	if res.ErrorCode != CodeRateLimited {
		t.Fatalf("expected rate_limited, got %s", res.ErrorCode)
	}
}

func TestProbeRunChannel_5xx(t *testing.T) {
	fx := newProbeFixture(t, "500")
	res := fx.probe.RunChannel(context.Background(), fx.channel.ID)
	if res.ErrorCode != CodeServer {
		t.Fatalf("expected server_error, got %s", res.ErrorCode)
	}
}

func TestProbeRunChannel_BadResponse(t *testing.T) {
	fx := newProbeFixture(t, "garbage")
	res := fx.probe.RunChannel(context.Background(), fx.channel.ID)
	if res.ErrorCode != CodeBadResponse {
		t.Fatalf("expected bad_response, got %s", res.ErrorCode)
	}
}

func TestProbeRunChannel_Timeout(t *testing.T) {
	fx := newProbeFixture(t, "slow")
	// Override timeout to be smaller than the stub's 2s sleep
	fx.probe.cfg.HTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	fx.probe.cfg.Timeout = 200 * time.Millisecond
	res := fx.probe.RunChannel(context.Background(), fx.channel.ID)
	if res.OK {
		t.Fatalf("should fail")
	}
	// Either CodeTimeout (ctx) or CodeNetwork (Client.Timeout) — both
	// acceptable signals; tests of "did not block forever" matter more.
	if res.ErrorCode != CodeTimeout && res.ErrorCode != CodeNetwork {
		t.Fatalf("expected timeout/network, got %s (%s)", res.ErrorCode, res.Error)
	}
}

func TestProbeRunCredential_OK(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	res := fx.probe.RunCredential(context.Background(), fx.cred.ID, "gpt-4o-mini")
	if !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
}

func TestProbeRunCredential_FallbackTestModel(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	// Empty test model → falls back to default
	res := fx.probe.RunCredential(context.Background(), fx.cred.ID, "")
	if !res.OK {
		t.Fatalf("expected ok with default model, got %+v", res)
	}
}

func TestProbeRunCredential_InvalidStatusStillProbes(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	ctx := context.Background()

	// Mark credential invalid — RevealForProbe should still decrypt
	_, _ = fx.pool.Exec(ctx,
		`UPDATE model_relay.credentials SET status='invalid' WHERE id=$1`, fx.cred.ID)

	res := fx.probe.RunCredential(ctx, fx.cred.ID, "gpt-4o-mini")
	if !res.OK {
		t.Fatalf("expected ok even for invalid cred, got %+v", res)
	}
}

func TestProbeRunCredential_NotFound(t *testing.T) {
	fx := newProbeFixture(t, "ok")
	res := fx.probe.RunCredential(context.Background(), uuid.New(), "gpt-4o-mini")
	if res.OK {
		t.Fatalf("should fail")
	}
}

func TestProbeNewPanicsOnNilDeps(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic")
		}
	}()
	_ = New(Config{})
}

// Sanity: classifyStatus maps ranges correctly.
func TestClassifyStatus(t *testing.T) {
	cases := map[int]string{
		200: CodeBadResponse, // 200 is "OK" path; classifyStatus only used for non-2xx
		401: CodeUnauthorized,
		403: CodeUnauthorized,
		429: CodeRateLimited,
		500: CodeServer,
		503: CodeServer,
		418: CodeBadResponse,
	}
	for code, want := range cases {
		if got := classifyStatus(code); got != want {
			t.Errorf("classifyStatus(%d) = %s want %s", code, got, want)
		}
	}
}

var _ = errors.New // keep import for use below

func TestStringPrefix(t *testing.T) {
	if !stringPrefix("hello world", "hello") {
		t.Fatal("prefix match failed")
	}
}
