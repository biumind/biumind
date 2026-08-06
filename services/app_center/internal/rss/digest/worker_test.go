package digest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseDigest_Plain(t *testing.T) {
	r, err := parseDigest(`{"takeaway":"AI 监管升级","bullets":["欧盟过审","美国跟进","中国预案"],"importance":3,"lang":"zh","topics":["AI","政策"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Takeaway != "AI 监管升级" || len(r.Bullets) != 3 || r.Importance != 3 || r.Lang != "zh" || len(r.Topics) != 2 {
		t.Errorf("rule = %+v", r)
	}
}

func TestParseDigest_StripsMarkdownFences(t *testing.T) {
	raw := "```json\n{\"takeaway\":\"x\",\"bullets\":[\"a\"],\"importance\":1,\"lang\":\"zh\",\"topics\":[\"AI\"]}\n```"
	r, err := parseDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Takeaway != "x" {
		t.Errorf("takeaway = %q", r.Takeaway)
	}
}

func TestParseDigest_ToleratesPrefixSuffix(t *testing.T) {
	raw := `好的, 总结如下: {"takeaway":"x","bullets":["a"]} 希望对你有帮助.`
	r, err := parseDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Takeaway != "x" {
		t.Errorf("takeaway = %q", r.Takeaway)
	}
}

func TestParseDigest_RejectsEmptyTakeaway(t *testing.T) {
	_, err := parseDigest(`{"takeaway":"","bullets":["a"]}`)
	if err == nil {
		t.Error("empty takeaway should fail")
	}
}

func TestParseDigest_NormalisesImportance(t *testing.T) {
	r, _ := parseDigest(`{"takeaway":"x","bullets":["a"],"importance":99}`)
	if r.Importance != 1 {
		t.Errorf("out-of-range importance should fall back to 1, got %d", r.Importance)
	}
	r, _ = parseDigest(`{"takeaway":"x","bullets":["a"],"importance":-5}`)
	if r.Importance != 1 {
		t.Errorf("negative importance should clamp to 1, got %d", r.Importance)
	}
}

func TestParseDigest_NormalisesLang(t *testing.T) {
	r, _ := parseDigest(`{"takeaway":"x","bullets":["a"],"lang":"FR"}`)
	if r.Lang != "zh" {
		t.Errorf("unknown lang should fall back zh, got %q", r.Lang)
	}
}

func TestParseDigest_DedupesAndTrims(t *testing.T) {
	r, _ := parseDigest(`{"takeaway":"x","bullets":[" a ","a","b","",""],"topics":["AI"," AI ","政策"]}`)
	if len(r.Bullets) != 2 || r.Bullets[0] != "a" || r.Bullets[1] != "b" {
		t.Errorf("bullets = %+v", r.Bullets)
	}
	if len(r.Topics) != 2 {
		t.Errorf("topics = %+v", r.Topics)
	}
}

func TestParseDigest_CapsBullets3(t *testing.T) {
	r, _ := parseDigest(`{"takeaway":"x","bullets":["a","b","c","d","e"]}`)
	if len(r.Bullets) != 3 {
		t.Errorf("bullets should cap 3, got %d", len(r.Bullets))
	}
}

func TestCallMessages_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer u-tok" {
			t.Errorf("auth missing/wrong")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": `{"takeaway":"AI 升级","bullets":["a","b","c"],"importance":2,"lang":"zh","topics":["AI"]}`},
			},
		})
	}))
	defer srv.Close()

	w := New(nil, srv.URL)
	r, err := w.callMessages(context.Background(), []byte(`{}`), "u-tok")
	if err != nil {
		t.Fatal(err)
	}
	if r.Takeaway != "AI 升级" || r.Importance != 2 {
		t.Errorf("result = %+v", r)
	}
}

func TestCallMessages_AuthErrorPermFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	defer srv.Close()

	w := New(nil, srv.URL)
	_, err := w.callMessages(context.Background(), []byte(`{}`), "u-tok")
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Errorf("expected auth error, got %v", err)
	}
}

func TestCallMessages_500RetryEligible(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := New(nil, srv.URL)
	_, err := w.callMessages(context.Background(), []byte(`{}`), "u-tok")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got %v", err)
	}
	// callMessages itself doesn't retry — callOnce does. Just verify
	// we got one call here.
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestContentHash_Stable(t *testing.T) {
	h1 := contentHash("hello world")
	h2 := contentHash("hello world")
	h3 := contentHash("Hello World")
	if h1 != h2 {
		t.Error("same content should hash the same")
	}
	if h1 == h3 {
		t.Error("different content should hash differently")
	}
	if len(h1) != 64 {
		t.Errorf("hash len = %d, want 64 hex", len(h1))
	}
}
