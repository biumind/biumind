// DefaultModelResolver 单测 —— httptest fake relay 覆盖兜底链顺序 /
// 缓存 / 禁用,不打真 relay。

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type fakeModelRelay struct {
	srv   *httptest.Server
	calls atomic.Int32

	token      string
	defStatus  int
	defCode    string
	prefStatus int
	prefCode   string
}

func newFakeModelRelay(t *testing.T, token string) *fakeModelRelay {
	t.Helper()
	f := &fakeModelRelay{token: token,
		defStatus: http.StatusNotFound, prefStatus: http.StatusNotFound}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		status, code := f.defStatus, f.defCode
		switch r.URL.Path {
		case "/v1/internal/models/default-chat":
		case "/v1/internal/models/preferred-chat":
			status, code = f.prefStatus, f.prefCode
		default:
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func testModelResolver(relayURL, envOverride string) *DefaultModelResolver {
	return NewDefaultModelResolver(relayURL, "internal-token", envOverride,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// 兜底链顺序: default-chat > env 覆盖 > preferred-chat > 明确报错。
func TestDefaultModelResolver_ChainOrder(t *testing.T) {
	f := newFakeModelRelay(t, "internal-token")

	// default-chat 命中 → 赢过 env 与 preferred。
	f.defStatus, f.defCode = http.StatusOK, "relay-default"
	f.prefStatus, f.prefCode = http.StatusOK, "relay-preferred"
	r := testModelResolver(f.srv.URL, "env-override")
	if got, err := r.Resolve(context.Background()); err != nil || got != "relay-default" {
		t.Fatalf("default-chat should win; got %q err=%v", got, err)
	}

	// default-chat 404 → env 覆盖。
	f.defStatus = http.StatusNotFound
	r = testModelResolver(f.srv.URL, "env-override")
	if got, err := r.Resolve(context.Background()); err != nil || got != "env-override" {
		t.Fatalf("env override expected; got %q err=%v", got, err)
	}

	// default-chat 404 + env 空 → preferred-chat。
	r = testModelResolver(f.srv.URL, "")
	if got, err := r.Resolve(context.Background()); err != nil || got != "relay-preferred" {
		t.Fatalf("preferred-chat expected; got %q err=%v", got, err)
	}

	// 全部落空 → ErrNoDefaultChatModel。
	f.prefStatus = http.StatusNotFound
	r = testModelResolver(f.srv.URL, "")
	if got, err := r.Resolve(context.Background()); !errors.Is(err, ErrNoDefaultChatModel) || got != "" {
		t.Fatalf("expected ErrNoDefaultChatModel; got %q err=%v", got, err)
	}
}

// TTL 缓存: 第二次调用命中缓存不再打 relay。
func TestDefaultModelResolver_Cached(t *testing.T) {
	f := newFakeModelRelay(t, "internal-token")
	f.defStatus, f.defCode = http.StatusOK, "m1"
	r := testModelResolver(f.srv.URL, "")
	if _, err := r.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.calls.Load(); n != 1 {
		t.Errorf("expect 1 relay call (cache hit on 2nd); got %d", n)
	}
}

// relayURL / token 空 → HTTP 两级禁用,只剩 env;env 也空 → 报错。
func TestDefaultModelResolver_Disabled(t *testing.T) {
	r := NewDefaultModelResolver("", "tok", "env-only", nil)
	if got, err := r.Resolve(context.Background()); err != nil || got != "env-only" {
		t.Fatalf("env-only expected; got %q err=%v", got, err)
	}
	r = NewDefaultModelResolver("http://relay:7001", "", "", nil)
	if _, err := r.Resolve(context.Background()); !errors.Is(err, ErrNoDefaultChatModel) {
		t.Fatalf("disabled resolver without env should fail; got %v", err)
	}
}
