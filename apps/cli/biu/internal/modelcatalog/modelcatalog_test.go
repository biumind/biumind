package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListParsesDefaultFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me/models" {
			t.Errorf("path=%s want /v1/me/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization=%q", got)
		}
		if r.URL.Query().Get("status") != "active" {
			t.Errorf("status=%q want active", r.URL.Query().Get("status"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"code":"claude-sonnet-4-6","display_name":"Sonnet","mode":"chat","is_default_chat":true},
			{"code":"kimi-k3","display_name":"Kimi","mode":"chat"},
			{"code":"text-embed-3","display_name":"Embed","mode":"embedding"}
		]}`))
	}))
	defer srv.Close()

	models, err := List(context.Background(), srv.URL+"/", "tok")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models", len(models))
	}

	code, err := DefaultChat(models)
	if err != nil {
		t.Fatalf("DefaultChat: %v", err)
	}
	if code != "claude-sonnet-4-6" {
		t.Errorf("default=%q", code)
	}

	chat := ChatModels(models)
	if len(chat) != 2 {
		t.Errorf("ChatModels=%d want 2 (embedding excluded)", len(chat))
	}
}

func TestDefaultChatNoDefault(t *testing.T) {
	if _, err := DefaultChat([]Model{{Code: "a"}, {Code: "b"}}); err != ErrNoDefault {
		t.Errorf("err=%v want ErrNoDefault", err)
	}
}

func TestListHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := List(context.Background(), srv.URL, "bad"); err == nil {
		t.Fatal("want error on 401")
	}
}
