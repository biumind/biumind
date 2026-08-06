package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biumind/biumind/services/authz/internal/cache"
	"github.com/biumind/biumind/services/authz/internal/engine"
)

const samplePolicies = `
@id("p_owner_read")
permit(
    principal,
    action == Action::"wiki:Page::read",
    resource is Page
) when { resource.owner == principal.id };
`

func newSrv(t *testing.T) *Server {
	t.Helper()
	e := engine.New()
	if err := e.LoadPolicies([]byte(samplePolicies)); err != nil {
		t.Fatal(err)
	}
	c, err := cache.New(100, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Engine: e, Cache: c, Logger: slog.Default()}
}

func TestCheckAllow(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)

	body, _ := json.Marshal(checkRequest{
		Principal: principalIn{Type: "User", ID: "u-1", Attributes: map[string]any{"id": "u-1"}},
		Action:    "wiki:Page::read",
		Resource:  resourceIn{Type: "Page", ID: "p-1", Attributes: map[string]any{"owner": "u-1"}},
	})
	req := httptest.NewRequest("POST", "/v1/authz/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var resp checkResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Decision != "ALLOW" {
		t.Errorf("got %s want ALLOW", resp.Decision)
	}
	if resp.FromCache {
		t.Errorf("first call should not be from cache")
	}
}

func TestCheckCacheHit(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)

	body, _ := json.Marshal(checkRequest{
		Principal: principalIn{Type: "User", ID: "u-1", Attributes: map[string]any{"id": "u-1"}},
		Action:    "wiki:Page::read",
		Resource:  resourceIn{Type: "Page", ID: "p-1", Attributes: map[string]any{"owner": "u-1"}},
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/v1/authz/check", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		var resp checkResponse
		_ = json.NewDecoder(rr.Body).Decode(&resp)
		if i == 1 && !resp.FromCache {
			t.Fatalf("second call should hit cache; got %+v", resp)
		}
	}
}

func TestBatchCheck(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)

	body, _ := json.Marshal(batchCheckRequest{
		Principal: principalIn{Type: "User", ID: "u-1", Attributes: map[string]any{"id": "u-1"}},
		Items: []batchItem{
			{Action: "wiki:Page::read", Resource: resourceIn{Type: "Page", ID: "p-1", Attributes: map[string]any{"owner": "u-1"}}},
			{Action: "wiki:Page::read", Resource: resourceIn{Type: "Page", ID: "p-2", Attributes: map[string]any{"owner": "u-2"}}},
		},
	})
	req := httptest.NewRequest("POST", "/v1/authz/batch_check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var resp batchCheckResponse
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Decisions) != 2 {
		t.Fatalf("expected 2 decisions; got %d", len(resp.Decisions))
	}
	if resp.Decisions[0].Decision != "ALLOW" {
		t.Errorf("first item: %s", resp.Decisions[0].Decision)
	}
	if resp.Decisions[1].Decision != "DENY" {
		t.Errorf("second item: %s", resp.Decisions[1].Decision)
	}
}

func TestPolicyMeta(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest("GET", "/v1/authz/policies", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestMissingActionDenied(t *testing.T) {
	s := newSrv(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	body, _ := json.Marshal(checkRequest{
		Principal: principalIn{ID: "u-1"},
		Action:    "",
		Resource:  resourceIn{ID: "x"},
	})
	req := httptest.NewRequest("POST", "/v1/authz/check", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500; got %d body=%s", rr.Code, rr.Body.String())
	}
}
