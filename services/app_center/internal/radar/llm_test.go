package radar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLLMRule_Plain(t *testing.T) {
	r, err := parseLLMRule(`{"name":"AI 新模型","match_any":["OpenAI","GPT"],"match_all":[],"exclude":["招聘"],"on_hit_badge":"warn","cooldown_sec":1800}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "AI 新模型" || len(r.MatchAny) != 2 || r.OnHitBadge != "warn" || r.CooldownSec != 1800 {
		t.Errorf("rule = %+v", r)
	}
}

func TestParseLLMRule_StripsMarkdownFences(t *testing.T) {
	raw := "```json\n{\"name\":\"x\",\"match_any\":[\"a\"]}\n```"
	r, err := parseLLMRule(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "x" {
		t.Errorf("name = %q", r.Name)
	}
}

func TestParseLLMRule_ToleratesPrefixSuffixText(t *testing.T) {
	raw := `好的，这是规则：{"match_any":["OpenAI"]} 希望对你有帮助。`
	r, err := parseLLMRule(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "OpenAI" {
		t.Errorf("expected name fallback to first keyword, got %q", r.Name)
	}
}

func TestParseLLMRule_RejectsEmptyKeywords(t *testing.T) {
	_, err := parseLLMRule(`{"name":"x","match_any":[],"match_all":[]}`)
	if err == nil {
		t.Error("empty rule should fail")
	}
}

func TestParseLLMRule_NormalisesBadge(t *testing.T) {
	r, _ := parseLLMRule(`{"match_any":["x"],"on_hit_badge":"CRITICAL"}`)
	if r.OnHitBadge != "warn" {
		t.Errorf("invalid badge should default to warn, got %q", r.OnHitBadge)
	}
	r, _ = parseLLMRule(`{"match_any":["x"],"on_hit_badge":"ERROR"}`)
	if r.OnHitBadge != "error" {
		t.Errorf("uppercase ERROR should normalise to error, got %q", r.OnHitBadge)
	}
}

func TestParseLLMRule_ClampsCooldown(t *testing.T) {
	r, _ := parseLLMRule(`{"match_any":["x"],"cooldown_sec":-100}`)
	if r.CooldownSec != 0 {
		t.Errorf("negative clamp = %d", r.CooldownSec)
	}
	r, _ = parseLLMRule(`{"match_any":["x"],"cooldown_sec":999999}`)
	if r.CooldownSec != 86400 {
		t.Errorf("upper clamp = %d", r.CooldownSec)
	}
}

func TestParseLLMRule_DedupesKeywords(t *testing.T) {
	r, _ := parseLLMRule(`{"match_any":["AI","AI"," AI ","",""],"exclude":[" 招聘 ","招聘"]}`)
	if len(r.MatchAny) != 1 || r.MatchAny[0] != "AI" {
		t.Errorf("match_any = %+v", r.MatchAny)
	}
	if len(r.Exclude) != 1 || r.Exclude[0] != "招聘" {
		t.Errorf("exclude = %+v", r.Exclude)
	}
}

func TestLLMClient_FromNL_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-tok" {
			t.Errorf("auth header missing/wrong: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": `{"name":"OpenAI 新模型","match_any":["OpenAI","GPT"],"on_hit_badge":"warn","cooldown_sec":1800}`},
			},
		})
	}))
	defer srv.Close()

	c := NewLLMClient(srv.URL)
	r, err := c.FromNL(context.Background(), "test-tok", "凡是 OpenAI 发布新模型的事都通知我")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "OpenAI 新模型" || len(r.MatchAny) != 2 {
		t.Errorf("rule = %+v", r)
	}
}

func TestLLMClient_FromNL_BlankBaseURL(t *testing.T) {
	c := NewLLMClient("")
	_, err := c.FromNL(context.Background(), "tok", "x")
	if err != ErrLLMUnavailable {
		t.Errorf("expected ErrLLMUnavailable, got %v", err)
	}
}

func TestLLMClient_FromNL_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream broke"}`))
	}))
	defer srv.Close()
	_, err := NewLLMClient(srv.URL).FromNL(context.Background(), "t", "x")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got %v", err)
	}
}
