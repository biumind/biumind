package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// fetchOpenAI base 不含 /v1(用户填代理根,如 https://new-api.example.com/)
// → 自动补 /v1,请求打到 /v1/models(不是 /models,后者打到 web 首页返 HTML)。
func TestFetchOpenAI_AutoV1Suffix(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-4o", "created": 1234},
				{"id": "gpt-4o-mini", "created": 5678},
			},
		})
	}))
	defer srv.Close()
	// srv.URL 形如 http://127.0.0.1:port,无 /v1(模拟用户填代理根)。
	rows, err := fetchOpenAI(context.Background(), srv.URL, "sk-test",
		uuid.New(), "openai")
	if err != nil {
		t.Fatalf("fetchOpenAI: %v", err)
	}
	if hitPath != "/v1/models" {
		t.Errorf("base 无 /v1 应自动补 → 请求 /v1/models;实际 %s", hitPath)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d want 2", len(rows))
	}
	if rows[0].ModelID != "gpt-4o" {
		t.Errorf("rows[0].ModelID=%q want gpt-4o", rows[0].ModelID)
	}
}

// fetchOpenAI base 已含 /v1(标准 api.openai.com/v1)→ 不重复补。
func TestFetchOpenAI_AlreadyV1(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "x"}},
		})
	}))
	defer srv.Close()
	_, err := fetchOpenAI(context.Background(), srv.URL+"/v1", "sk-test",
		uuid.New(), "openai")
	if err != nil {
		t.Fatalf("fetchOpenAI: %v", err)
	}
	if hitPath != "/v1/models" {
		t.Errorf("base 含 /v1 应直接 /v1/models;实际 %s", hitPath)
	}
}

// fetchOpenAI base 含末尾斜杠 + 无 /v1 → TrimRight 后补 /v1。
func TestFetchOpenAI_TrailingSlashNoV1(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "x"}}})
	}))
	defer srv.Close()
	// 模拟 https://new-api.example.com/(末尾 / + 无 /v1)。
	_, err := fetchOpenAI(context.Background(), srv.URL+"/", "sk-test",
		uuid.New(), "custom")
	if err != nil {
		t.Fatalf("fetchOpenAI: %v", err)
	}
	if hitPath != "/v1/models" {
		t.Errorf("trailing-slash base 应补 /v1/models;实际 %s", hitPath)
	}
}
