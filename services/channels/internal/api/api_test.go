package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/channels/internal/driver"
	"github.com/biumind/biumind/services/channels/internal/envelope"
	"github.com/biumind/biumind/services/channels/internal/router"
)

const (
	testSecret = "test-secret-very-long-string-for-hmac-32"
	testIss    = "iss"
	testAud    = "aud"
)

func newSrv(t *testing.T) (*Server, *router.Router, *driver.Stub) {
	t.Helper()
	stub := driver.NewStub()
	rt := router.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	rt.Register(stub)
	v := bauth.NewVerifier(testSecret, testIss, testAud)
	srv := NewServer(rt, v, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return srv, rt, stub
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

func TestWebhookRoutesInbound(t *testing.T) {
	srv, rt, _ := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)

	body := []byte(`{"text":"hi","sender":{"platform_id":"alice"},"conversation_id":"c1"}`)
	req := httptest.NewRequest("POST", "/v1/channels/stub/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("webhook: %d %s", rr.Code, rr.Body.String())
	}
	recent := rt.Recent(10)
	if len(recent) != 1 {
		t.Fatalf("router didn't record envelope: %d", len(recent))
	}
	if recent[0].Channel != "stub" || recent[0].Direction != envelope.DirectionInbound {
		t.Errorf("bad envelope: %+v", recent[0])
	}
}

func TestWebhookUnknownChannel(t *testing.T) {
	srv, _, _ := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	req := httptest.NewRequest("POST", "/v1/channels/wechat/webhook",
		bytes.NewReader([]byte(`{}`)))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rr.Code)
	}
}

func TestSendRequiresAuth(t *testing.T) {
	srv, _, _ := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	body := []byte(`{"channel":"stub","conversation_id":"c1","text":"hi"}`)
	req := httptest.NewRequest("POST", "/v1/channels/send", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestSendRoundtrip(t *testing.T) {
	srv, _, stub := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-1")

	body := []byte(`{"channel":"stub","conversation_id":"c1","text":"hello world"}`)
	req := httptest.NewRequest("POST", "/v1/channels/send", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rr.Code, rr.Body.String())
	}
	var out envelope.Envelope
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Direction != envelope.DirectionOutbound || !strings.HasPrefix(out.MessageID, "stub-out-") {
		t.Errorf("bad outbound: %+v", out)
	}
	if len(stub.Sent) != 1 || stub.Sent[0].Text != "hello world" {
		t.Errorf("stub didn't record send: %+v", stub.Sent)
	}
}

func TestSendUnknownChannel(t *testing.T) {
	srv, _, _ := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-1")
	body := []byte(`{"channel":"discord","conversation_id":"c1","text":"hi"}`)
	req := httptest.NewRequest("POST", "/v1/channels/send", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rr.Code)
	}
}

func TestListAndRecentEndpoints(t *testing.T) {
	srv, rt, _ := newSrv(t)
	mux := http.NewServeMux()
	srv.Mount(mux)
	tok := mintToken(t, "u-1")

	// seed an envelope
	rt.Inbound(nil, []envelope.Envelope{{
		MessageID: "m1", Channel: "stub",
		Direction: envelope.DirectionInbound,
		Text:      "seed",
	}})

	req := httptest.NewRequest("GET", "/v1/channels", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"stub"`) {
		t.Errorf("/v1/channels missing stub: %s", rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/v1/channels/recent?n=10", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), `"seed"`) {
		t.Errorf("/v1/channels/recent missing envelope: %s", rr.Body.String())
	}
}
