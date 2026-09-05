package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStub_Deterministic(t *testing.T) {
	e := NewStub(64)
	a, err := e.Embed(context.Background(), "deploy via biu push")
	if err != nil {
		t.Fatalf("embed a: %v", err)
	}
	b, _ := e.Embed(context.Background(), "deploy via biu push")
	if len(a) != 64 || len(b) != 64 {
		t.Fatalf("dim mismatch: %d / %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("non-deterministic at %d: %v vs %v", i, a[i], b[i])
			break
		}
	}
}

func TestStub_DifferentTextsDifferentVectors(t *testing.T) {
	e := NewStub(128)
	a, _ := e.Embed(context.Background(), "rust programming")
	b, _ := e.Embed(context.Background(), "go programming")
	identical := true
	for i := range a {
		if a[i] != b[i] {
			identical = false
			break
		}
	}
	if identical {
		t.Error("disjoint texts produced identical vectors")
	}
}

func TestStub_NormalisedToUnitLength(t *testing.T) {
	e := NewStub(256)
	v, _ := e.Embed(context.Background(), "the quick brown fox jumps over the lazy dog")
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	got := math.Sqrt(sum)
	if math.Abs(got-1.0) > 1e-5 {
		t.Errorf("expected L2 norm == 1.0, got %v", got)
	}
}

// Two texts sharing the keyword "vim" must land closer in cosine
// space than two with no token overlap. This is what makes the stub
// usable for end-to-end pipeline tests of recall ranking.
func TestStub_SharedTokensClusterCloser(t *testing.T) {
	e := NewStub(512)
	a, _ := e.Embed(context.Background(), "uses vim and tmux for editing")
	b, _ := e.Embed(context.Background(), "vim is the best editor")
	c, _ := e.Embed(context.Background(), "rust async runtimes")

	// a · b should exceed a · c (more shared tokens → higher cosine)
	ab := dot(a, b)
	ac := dot(a, c)
	if ab <= ac {
		t.Errorf("expected vim-overlap to dominate: a·b=%v vs a·c=%v", ab, ac)
	}
}

func TestStub_EmptyStringDoesNotPanic(t *testing.T) {
	e := NewStub(32)
	v, err := e.Embed(context.Background(), "")
	if err != nil {
		t.Fatalf("empty embed: %v", err)
	}
	if len(v) != 32 {
		t.Errorf("dim: %d", len(v))
	}
	// Should be normalised even for empty input — non-zero unit vector.
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(sum)-1.0) > 1e-5 {
		t.Errorf("empty input not normalised: norm=%v", math.Sqrt(sum))
	}
}

func TestOpenAI_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		var req openAIRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "text-embedding-3-small" {
			t.Errorf("model: %s", req.Model)
		}
		// Return a fixed-length vector matching requested dims.
		v := make([]float32, req.Dimensions)
		for i := range v {
			v[i] = float32(i) / float32(req.Dimensions)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": v, "index": 0}},
			"model": req.Model,
			"usage": map[string]int{"prompt_tokens": 5, "total_tokens": 5},
		})
	}))
	defer srv.Close()

	e, err := NewOpenAI(OpenAIConfig{
		BaseURL: srv.URL, APIKey: "test-key", Dims: 8,
	})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	v, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(v) != 8 {
		t.Errorf("dim: %d", len(v))
	}
	if e.Model() != "text-embedding-3-small" {
		t.Errorf("model: %s", e.Model())
	}
}

func TestOpenAI_PropagatesProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "model not found", "type": "invalid_request"},
		})
	}))
	defer srv.Close()

	e, _ := NewOpenAI(OpenAIConfig{
		BaseURL: srv.URL, APIKey: "x", Dims: 4,
	})
	_, err := e.Embed(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected status in error: %v", err)
	}
}

func TestOpenAI_RejectsDimMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return 4 dims regardless of request — simulate a
		// misconfigured proxy that ignores the dimensions param.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": []float32{1, 2, 3, 4}, "index": 0}},
			"model": "broken",
		})
	}))
	defer srv.Close()

	e, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "x", Dims: 8})
	_, err := e.Embed(context.Background(), "x")
	if err == nil {
		t.Fatal("expected dim mismatch error")
	}
	if !strings.Contains(err.Error(), "dims") {
		t.Errorf("expected dim error message: %v", err)
	}
}

func TestOpenAI_ConstructorRequiresAPIKey(t *testing.T) {
	if _, err := NewOpenAI(OpenAIConfig{}); err == nil {
		t.Error("want error for missing API key")
	}
}

// ─── helpers ────────────────────────────────────────────

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// ─── EmbedBatch ─────────────────────────────────────────

func TestOpenAI_EmbedBatchHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input      []string `json:"input"`
			Model      string   `json:"model"`
			Dimensions int      `json:"dimensions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Input) != 3 {
			t.Errorf("batch input len = %d, want 3 (array input must reach the wire)", len(req.Input))
		}
		// Return vectors OUT OF ORDER — the client must honour the
		// index field, not array position.
		mk := func(seed float32) []float32 {
			v := make([]float32, req.Dimensions)
			for i := range v {
				v[i] = seed
			}
			return v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": mk(2), "index": 2},
				{"embedding": mk(0), "index": 0},
				{"embedding": mk(1), "index": 1},
			},
			"model": req.Model,
			"usage": map[string]int{"prompt_tokens": 9, "total_tokens": 9},
		})
	}))
	defer srv.Close()

	e, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Dims: 4})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("vectors = %d, want 3", len(vecs))
	}
	for i, want := range []float32{0, 1, 2} {
		if vecs[i][0] != want {
			t.Errorf("vec %d = %v, want seed %v (index ordering)", i, vecs[i][0], want)
		}
	}
}

func TestOpenAI_EmbedBatchRejectsCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": []float32{1, 2, 3, 4}, "index": 0}},
			"model": "m",
		})
	}))
	defer srv.Close()

	e, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Dims: 4})
	_, err := e.EmbedBatch(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected count-mismatch error")
	}
	if !strings.Contains(err.Error(), "vectors for 2 inputs") {
		t.Errorf("error = %v, want count mismatch", err)
	}
}

func TestOpenAI_EmbedBatchEmpty(t *testing.T) {
	e, _ := NewOpenAI(OpenAIConfig{BaseURL: "http://127.0.0.1:1", APIKey: "k", Dims: 4})
	vecs, err := e.EmbedBatch(context.Background(), nil)
	if err != nil || vecs != nil {
		t.Errorf("empty batch = (%v, %v), want (nil, nil) — no HTTP call", vecs, err)
	}
}

func TestOpenAI_SingleEmbedStillWorks(t *testing.T) {
	// Single Embed must keep sending a STRING input (OpenAI wire shape),
	// not a one-element array.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input json.RawMessage `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var s string
		if err := json.Unmarshal(req.Input, &s); err != nil {
			t.Errorf("single Embed input is not a string: %s", req.Input)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"embedding": []float32{1, 2, 3, 4}, "index": 0}},
			"model": "m",
		})
	}))
	defer srv.Close()

	e, _ := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, APIKey: "k", Dims: 4})
	v, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(v) != 4 {
		t.Errorf("dim = %d, want 4", len(v))
	}
}

func TestStub_EmbedBatchMatchesSingles(t *testing.T) {
	e := NewStub(64)
	texts := []string{"alpha beta", "gamma", "delta epsilon zeta"}
	batch, err := e.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(batch) != len(texts) {
		t.Fatalf("vectors = %d, want %d", len(batch), len(texts))
	}
	for i, text := range texts {
		single, _ := e.Embed(context.Background(), text)
		for j := range single {
			if batch[i][j] != single[j] {
				t.Fatalf("batch[%d] diverges from single Embed at dim %d", i, j)
			}
		}
	}
}
