package files

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newFakeBrain(t *testing.T, presignBody string, presignStatus int,
	fetchBody string, fetchStatus int, fetchCT string,
) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/files/{id}/presign-get", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(presignStatus)
		_, _ = w.Write([]byte(presignBody))
	})
	mux.HandleFunc("/v1/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		if fetchCT != "" {
			w.Header().Set("Content-Type", fetchCT)
		}
		w.WriteHeader(fetchStatus)
		_, _ = w.Write([]byte(fetchBody))
	})
	return httptest.NewServer(mux)
}

func TestHTTPResolver_PresignURL_HappyPath(t *testing.T) {
	srv := newFakeBrain(t,
		`{"url":"https://minio.example/abc?sig=1","media_type":"image/png"}`, 200,
		"", 0, "")
	defer srv.Close()

	r := NewHTTPResolver(srv.URL)
	ctx := WithBearerToken(context.Background(), "tok-xyz")
	url, mt, err := r.PresignURL(ctx, "fid-1")
	if err != nil {
		t.Fatalf("PresignURL: %v", err)
	}
	if !strings.HasPrefix(url, "https://minio.example/abc") {
		t.Errorf("url: %s", url)
	}
	if mt != "image/png" {
		t.Errorf("media_type: %s", mt)
	}
}

func TestHTTPResolver_PresignURL_RequiresBearer(t *testing.T) {
	r := NewHTTPResolver("http://brain.invalid")
	_, _, err := r.PresignURL(context.Background(), "fid")
	if err == nil || !strings.Contains(err.Error(), "bearer") {
		t.Errorf("expected bearer error, got %v", err)
	}
}

func TestHTTPResolver_PresignURL_404Surfaces(t *testing.T) {
	srv := newFakeBrain(t, `{"error":{"code":"not_found"}}`, 404, "", 0, "")
	defer srv.Close()
	r := NewHTTPResolver(srv.URL)
	ctx := WithBearerToken(context.Background(), "tok")
	_, _, err := r.PresignURL(ctx, "fid")
	if err == nil {
		t.Fatal("expected 404 error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got %v", err)
	}
}

func TestHTTPResolver_PresignURL_EmptyBaseURL(t *testing.T) {
	r := NewHTTPResolver("")
	ctx := WithBearerToken(context.Background(), "tok")
	_, _, err := r.PresignURL(ctx, "fid")
	if err == nil || !strings.Contains(err.Error(), "BrainBaseURL") {
		t.Errorf("expected BrainBaseURL error, got %v", err)
	}
}

func TestHTTPResolver_PresignURL_EmptyURLInResponse(t *testing.T) {
	srv := newFakeBrain(t, `{"url":""}`, 200, "", 0, "")
	defer srv.Close()
	r := NewHTTPResolver(srv.URL)
	ctx := WithBearerToken(context.Background(), "tok")
	_, _, err := r.PresignURL(ctx, "fid")
	if err == nil || !strings.Contains(err.Error(), "empty url") {
		t.Errorf("expected empty url error, got %v", err)
	}
}

func TestHTTPResolver_Fetch_HappyPath(t *testing.T) {
	srv := newFakeBrain(t,
		"", 0,
		"raw-bytes-here", 200, "image/jpeg")
	defer srv.Close()
	r := NewHTTPResolver(srv.URL)
	ctx := WithBearerToken(context.Background(), "tok")
	body, mt, err := r.Fetch(ctx, "fid")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != "raw-bytes-here" {
		t.Errorf("body: %q", body)
	}
	if mt != "image/jpeg" {
		t.Errorf("media: %q", mt)
	}
}

func TestHTTPResolver_PresignURL_DecodeFailure(t *testing.T) {
	srv := newFakeBrain(t, `not-json`, 200, "", 0, "")
	defer srv.Close()
	r := NewHTTPResolver(srv.URL)
	ctx := WithBearerToken(context.Background(), "tok")
	_, _, err := r.PresignURL(ctx, "fid")
	if err == nil {
		t.Fatal("expected decode error")
	}
}

// Smoke check that BearerFromContext / WithBearerToken are pure-fn correct.
func TestBearerCtxRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := BearerFromContext(ctx); ok {
		t.Errorf("empty ctx returned a token")
	}
	ctx = WithBearerToken(ctx, "abc")
	got, ok := BearerFromContext(ctx)
	if !ok || got != "abc" {
		t.Errorf("round-trip: got=%q ok=%v", got, ok)
	}
}

// Defensive: a Resolver implementation must satisfy the interface.
var _ Resolver = (*HTTPResolver)(nil)

// Static interface conformance test (compile-time guard).
func TestResolverInterfaceCompiles(t *testing.T) {
	var r Resolver = NewHTTPResolver("x")
	_ = r
	// Use json package so import doesn't unused-warn if test file shrinks.
	if _, err := json.Marshal(map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(errors.New("x"), errors.New("x")) {
		// purely to ensure errors import remains used
	}
}
