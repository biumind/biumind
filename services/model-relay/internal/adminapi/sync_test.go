package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// stubUpstream serves the basellm.github.io/llm-metadata schema with a
// configurable model list + ETag. Lets us drive sync without hitting
// real GitHub.
func stubUpstream(t *testing.T, models []upstreamModel, etag string) *httptest.Server {
	t.Helper()
	body, _ := json.Marshal(upstreamEnvelope{Data: models})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/newapi/models.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag && etag != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/api/newapi/vendors.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSyncUpstream_AddsNewModels(t *testing.T) {
	fx := newAdminFixture(t)
	ctx := context.Background()

	upstreamModels := []upstreamModel{
		{
			ModelName: fmt.Sprintf("e2e-claude-%d", time.Now().UnixNano()),
			VendorName: "Anthropic", Description: "haiku", Status: 1,
			Tags:                "Tools,Files,Vision,200K",
			PricePerMInput:      0.8, PricePerMOutput: 4.0,
			PricePerMCacheRead:  0.08, PricePerMCacheWrite: 1.0,
		},
		{
			ModelName: fmt.Sprintf("e2e-gpt-%d", time.Now().UnixNano()),
			VendorName: "OpenAI", Status: 1,
			Tags:           "Tools,Vision,128K",
			PricePerMInput: 5.0, PricePerMOutput: 15.0,
		},
		{
			ModelName: "duplicated_model_name",
			VendorName: "VendorA", Status: 1,
			Tags: "Tools,8K",
		},
		{
			ModelName: "duplicated_model_name", // dup
			VendorName: "VendorB", Status: 1,
		},
		{
			ModelName: "deactivated_model",
			VendorName: "Bad", Status: 0, // not active → skipped
		},
	}

	stub := stubUpstream(t, upstreamModels, `"v1"`)
	fx.server.SyncUpstreamURL = stub.URL

	resp, body := fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: %d body=%s", resp.StatusCode, body)
	}
	var res syncResponse
	decodeBody(t, body, &res)

	if res.Added < 3 {
		t.Fatalf("expected 3 added (2 unique + 1 dedupe winner), got %d", res.Added)
	}
	if res.Skipped < 2 {
		t.Fatalf("expected 2 skipped (1 dup + 1 inactive), got %d", res.Skipped)
	}
	if res.ETag == "" {
		t.Fatalf("etag not surfaced")
	}

	// Cleanup: remove the synced models
	defer func() {
		for _, m := range upstreamModels {
			_, _ = fx.pool.Exec(ctx,
				`DELETE FROM model_relay.models WHERE code=$1`, m.ModelName)
		}
	}()

	// Verify capability + context window mapping
	for _, code := range []string{upstreamModels[0].ModelName, upstreamModels[1].ModelName} {
		got, err := fx.store.Models.GetByCode(ctx, code)
		if err != nil {
			t.Fatalf("get %s: %v", code, err)
		}
		if !got.Capabilities.Tools || !got.Capabilities.Vision {
			t.Fatalf("%s: capabilities lost: %+v", code, got.Capabilities)
		}
		if got.ContextWindow == 0 {
			t.Fatalf("%s: context_window not parsed from tag", code)
		}
		if got.Status != registry.StatusDisabled {
			t.Fatalf("%s: synced models should land disabled, got %s", code, got.Status)
		}
		if got.UpstreamRef == nil {
			t.Fatalf("%s: upstream_ref not set", code)
		}
		// Pricing only inserted when upstream had non-zero values
		if upstreamModels[0].ModelName == code {
			pr, err := fx.store.Pricing.GetCurrent(ctx, got.ID)
			if err != nil {
				t.Fatalf("%s: pricing missing: %v", code, err)
			}
			if pr.InputPerMTok != 0.8 || pr.Currency != registry.CurrencyUSD {
				t.Fatalf("%s: pricing wrong: %+v", code, pr)
			}
		}
		// Default group binding
		groups, _ := fx.store.Groups.ListGroupsForModel(ctx, got.ID)
		if len(groups) != 1 || groups[0].ID != registry.DefaultGroupID {
			t.Fatalf("%s: not bound to default group", code)
		}
	}
}

func TestSyncUpstream_ETagShortCircuit(t *testing.T) {
	fx := newAdminFixture(t)

	mname := fmt.Sprintf("e2e-etag-%d", time.Now().UnixNano())
	stub := stubUpstream(t, []upstreamModel{
		{ModelName: mname, VendorName: "v", Status: 1, Tags: "Tools,8K"},
	}, `"etag-1"`)
	fx.server.SyncUpstreamURL = stub.URL
	defer fx.pool.Exec(context.Background(),
		"DELETE FROM model_relay.models WHERE code=$1", mname) //nolint:errcheck

	// First sync — populates cache.
	resp, body := fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first sync: %d body=%s", resp.StatusCode, body)
	}

	// Second sync — same upstream, ETag matches → not_modified.
	resp, body = fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second sync: %d body=%s", resp.StatusCode, body)
	}
	var res syncResponse
	decodeBody(t, body, &res)
	if !res.NotModified {
		t.Fatalf("expected not_modified on second call: %+v", res)
	}
	if res.Added != 0 || res.Updated != 0 {
		t.Fatalf("not_modified should not mutate: %+v", res)
	}
}

func TestSyncUpstream_RespectsManualOverride(t *testing.T) {
	fx := newAdminFixture(t)
	ctx := context.Background()

	mname := fmt.Sprintf("e2e-mo-%d", time.Now().UnixNano())

	// Existing model with manual_override=true and a custom display
	existing, err := fx.store.Models.Insert(ctx, registry.ModelInput{
		Code: mname, DisplayName: "ADMIN_PINNED",
		Family: "custom", Status: registry.StatusActive,
		MinPlan: registry.PlanFree, ManualOverride: true,
	})
	if err != nil {
		t.Fatalf("preset: %v", err)
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", existing.ID) //nolint:errcheck

	// Upstream tries to overwrite display + family + tags
	stub := stubUpstream(t, []upstreamModel{
		{
			ModelName: mname, VendorName: "Upstream",
			Description: "should not appear", Status: 1,
			Tags: "Tools,Vision,1M",
		},
	}, `"v2"`)
	fx.server.SyncUpstreamURL = stub.URL

	resp, body := fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: %d body=%s", resp.StatusCode, body)
	}
	var res syncResponse
	decodeBody(t, body, &res)
	if res.Skipped < 1 {
		t.Fatalf("manual_override row should be skipped: %+v", res)
	}

	// Verify the row is unchanged
	got, _ := fx.store.Models.GetByCode(ctx, mname)
	if got.DisplayName != "ADMIN_PINNED" {
		t.Fatalf("manual_override violated: display=%q", got.DisplayName)
	}
	if got.Family != "custom" {
		t.Fatalf("manual_override violated: family=%q", got.Family)
	}
}

func TestSyncUpstream_UpdatesNonOverriddenModels(t *testing.T) {
	fx := newAdminFixture(t)
	ctx := context.Background()

	mname := fmt.Sprintf("e2e-upd-%d", time.Now().UnixNano())

	// Existing model WITHOUT manual_override
	existing, err := fx.store.Models.Insert(ctx, registry.ModelInput{
		Code: mname, DisplayName: "old",
		Family: "stale", Status: registry.StatusActive,
		MinPlan: registry.PlanFree, ManualOverride: false,
	})
	if err != nil {
		t.Fatalf("preset: %v", err)
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", existing.ID) //nolint:errcheck

	stub := stubUpstream(t, []upstreamModel{
		{
			ModelName:  mname,
			VendorName: "Anthropic",
			Status:     1,
			Tags:       "Tools,Vision,Reasoning,200K",
		},
	}, `"v3"`)
	fx.server.SyncUpstreamURL = stub.URL

	resp, _ := fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: %d", resp.StatusCode)
	}

	got, _ := fx.store.Models.GetByCode(ctx, mname)
	if !got.Capabilities.Tools || !got.Capabilities.Vision || !got.Capabilities.Thinking {
		t.Fatalf("capabilities not updated: %+v", got.Capabilities)
	}
	if got.ContextWindow != 200_000 {
		t.Fatalf("context_window: %d", got.ContextWindow)
	}
	if got.Family != mname[:0] && got.Family != "other" && got.Family == "stale" {
		t.Fatalf("family not updated: %q", got.Family)
	}
}

func TestSyncUpstream_BadGateway(t *testing.T) {
	fx := newAdminFixture(t)

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream borked"))
	}))
	defer stub.Close()
	fx.server.SyncUpstreamURL = stub.URL

	resp, body := fx.do(t, "POST", "/v1/admin/models/sync-upstream", "admin", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", resp.StatusCode, body)
	}
}

func TestParseTags(t *testing.T) {
	cases := []struct {
		in        string
		wantCaps  registry.Capabilities
		wantWin   int
	}{
		{"Tools,Files,Vision,200K", registry.Capabilities{Tools: true, Vision: true}, 200_000},
		{"Reasoning,Tools,Vision,128K", registry.Capabilities{Tools: true, Vision: true, Thinking: true}, 128_000},
		{"1M", registry.Capabilities{}, 1_000_000},
		{"Audio,32k", registry.Capabilities{Audio: true}, 32_000},
		{"", registry.Capabilities{}, 0},
		{"unknown,gibberish", registry.Capabilities{}, 0},
	}
	for _, c := range cases {
		caps, win := parseTags(c.in)
		if caps != c.wantCaps || win != c.wantWin {
			t.Errorf("parseTags(%q) = (%+v, %d), want (%+v, %d)",
				c.in, caps, win, c.wantCaps, c.wantWin)
		}
	}
}

func TestFamilyFromName(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-4":         "claude",
		"gpt-4o":                  "openai",
		"chatgpt-4o-latest":       "openai",
		"o3-mini":                 "openai",
		"gemini-2.5-pro":          "gemini",
		"deepseek-chat":           "deepseek",
		"qwen-max":                "qwen",
		"llama-3.1-70b":           "llama",
		"kimi-k2":                 "kimi",
		"moonshot-v1-8k":          "kimi",
		"glm-4.5":                 "zhipu",
		"grok-2":                  "grok",
		"mistral-large":           "mistral",
		"random-internal-model":   "other",
	}
	for in, want := range cases {
		if got := familyFromName(in); got != want {
			t.Errorf("familyFromName(%q) = %q, want %q", in, got, want)
		}
	}
}
