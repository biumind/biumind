package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/deploy/internal/driver"
)

const (
	testSecret = "test-secret-very-long-string-for-hmac-32"
	testIss    = "iss"
	testAud    = "aud"
)

func newServer(t *testing.T) (*Server, *driver.Stub) {
	t.Helper()
	stub := driver.NewStub()
	v := bauth.NewVerifier(testSecret, testIss, testAud)
	srv := NewServer(stub, v, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return srv, stub
}

func mintToken(t *testing.T, userID string) string {
	t.Helper()
	s := bauth.NewSigner(testSecret, testIss, testAud, time.Hour)
	tok, err := s.Sign(&bauth.Claims{UserID: userID, DeviceID: "test"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func tinyTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("hi")
	_ = tw.WriteHeader(&tar.Header{
		Name: "index.html", Size: int64(len(body)), Mode: 0o644, Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// buildMultipart serialises a deploy multipart body. Returns (body, contentType).
func buildMultipart(t *testing.T, kind string, tarball []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("kind", kind)
	fw, _ := mw.CreateFormFile("tarball", "site.tgz")
	_, _ = fw.Write(tarball)
	mw.Close()
	return &body, mw.FormDataContentType()
}

func TestRequireAuth(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	body, ct := buildMultipart(t, "static", tinyTarball(t))
	req := httptest.NewRequest("POST", "/v1/deploys", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestDeployCreateGetDestroy(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-1")

	body, ct := buildMultipart(t, "static", tinyTarball(t))
	req := httptest.NewRequest("POST", "/v1/deploys", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("deploy: %d %s", rr.Code, rr.Body.String())
	}
	var dep map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &dep)
	id := dep["id"].(string)
	if !strings.HasPrefix(id, "stub-") {
		t.Errorf("bad id %q", id)
	}

	// get
	req = httptest.NewRequest("GET", "/v1/deploys/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("get: %d", rr.Code)
	}

	// destroy
	req = httptest.NewRequest("DELETE", "/v1/deploys/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("destroy: %d", rr.Code)
	}
}

func TestOwnerIsolation(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tokA := mintToken(t, "alice")
	tokB := mintToken(t, "bob")

	body, ct := buildMultipart(t, "static", tinyTarball(t))
	req := httptest.NewRequest("POST", "/v1/deploys", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+tokA)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var dep map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &dep)
	id := dep["id"].(string)

	req = httptest.NewRequest("DELETE", "/v1/deploys/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tokB)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-tenant destroy must 403, got %d", rr.Code)
	}
}

func TestDeployRequiresTarball(t *testing.T) {
	srv, _ := newServer(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-1")

	// multipart with kind but no tarball
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("kind", "static")
	mw.Close()

	req := httptest.NewRequest("POST", "/v1/deploys", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rr.Code)
	}
}
