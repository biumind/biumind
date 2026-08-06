package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
)

// Regression (P0): a stale cron trigger firing the legacy "digest" action
// against the PG-backed rss app used to dereference the nil in-memory store
// and SIGSEGV the whole app_center process. End-to-end at the HTTP layer:
// the invoke must return a graceful error response, never panic.
//
// No DB needed — NewWithPool(nil) makes a.pg non-nil, so the PG-mode guard
// fires and returns before touching the (nil) pool. With httptest a handler
// panic propagates into this goroutine and fails the test, so a clean
// response is itself proof the crash is gone.
func TestInvokeDigest_PGMode_NoPanic(t *testing.T) {
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(context.Background(), rss.NewWithPool(nil)); err != nil {
		t.Fatalf("register rss: %v", err)
	}
	v := bauth.NewVerifier(testSecret, testIss, testAud)
	srv := NewServer(reg, v, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)

	for _, action := range []string{"digest", "fetch"} {
		t.Run(action, func(t *testing.T) {
			body := []byte(`{"action":"` + action + `","input":{}}`)
			req := httptest.NewRequest("POST", "/v1/apps/rss/invoke",
				bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+mintToken(t))
			rr := httptest.NewRecorder()

			mux.ServeHTTP(rr, req) // must NOT panic

			// Graceful error response (any 4xx/5xx), with the guard message.
			if rr.Code < 400 || rr.Code >= 600 {
				t.Fatalf("expected an error status, got %d %s",
					rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "not supported in PG-backed mode") {
				t.Fatalf("expected guard message, got %d %s",
					rr.Code, rr.Body.String())
			}
		})
	}
}
