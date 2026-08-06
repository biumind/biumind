package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowedOriginEcho(t *testing.T) {
	c := Default()
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "http://x/y", nil)
	req.Header.Set("Origin", "https://app.biumind.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.biumind.com" {
		t.Errorf("ACAO = %q, want app.biumind.com echoed", got)
	}
}

func TestRejectedOrigin(t *testing.T) {
	c := Default()
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "http://x/y", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no ACAO for non-allowed origin")
	}
}

func TestPreflight204(t *testing.T) {
	c := Default()
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("OPTIONS should short-circuit, handler not called")
	}))
	req := httptest.NewRequest("OPTIONS", "http://x/y", nil)
	req.Header.Set("Origin", "https://app.biumind.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Errorf("missing allow-methods")
	}
	if rr.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Errorf("missing allow-headers")
	}
}

func TestExtraOriginAdded(t *testing.T) {
	c := Default("https://staging.biumind.com")
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "http://x/y", nil)
	req.Header.Set("Origin", "https://staging.biumind.com")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://staging.biumind.com" {
		t.Errorf("staging not allowed")
	}
}

func TestAllowAllWildcard(t *testing.T) {
	c := Config{AllowAll: true, AllowedMethods: []string{"GET"}, AllowedHeaders: []string{"*"}}
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "http://x/y", nil)
	req.Header.Set("Origin", "https://anywhere.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected *")
	}
	if rr.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("credentials must be empty when ACAO=*")
	}
}
