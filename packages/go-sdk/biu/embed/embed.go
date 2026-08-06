// Package embed defines the canonical text-embedding contract used
// across BiuMind services.
//
// Today there are two implementations:
//
//	OpenAIEmbedder  — calls /v1/embeddings on OpenAI-compatible APIs
//	                  (OpenAI, Azure, LiteLLM, llama.cpp, vLLM, etc.)
//	StubEmbedder    — deterministic hash-based vector for offline tests
//	                  and dev environments without an API key.
//
// All embedders return float32 slices of a fixed Dim() length so the
// pgvector column type stays stable across restarts.
//
// Brain.Memory's recall pipeline picks the embedder by env at boot:
//
//	EMBED_PROVIDER=openai  EMBED_API_KEY=…  EMBED_MODEL=text-embedding-3-small
//	EMBED_PROVIDER=stub    (default; no network)
package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Embedder is the contract every text-embedding backend implements.
type Embedder interface {
	// Embed returns a fixed-length vector. Implementations MUST return
	// a slice of exactly Dim() floats.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Dim is the output dimension. Stable across calls and restarts;
	// must match the pgvector column type that stores the result.
	Dim() int
	// Model is a free-form identifier (e.g. "text-embedding-3-small",
	// "stub-1536"). Logged for traceability.
	Model() string
}

// ─── OpenAI-compatible HTTP embedder ────────────────────

type OpenAIConfig struct {
	BaseURL string // default https://api.openai.com/v1
	APIKey  string // required
	Model   string // default text-embedding-3-small (1536 dims)
	Dims    int    // default 1536; must match the stored column
	HTTP    *http.Client
}

type openAIEmbedder struct {
	cfg OpenAIConfig
}

// NewOpenAI constructs an OpenAI-compatible embedder. Works against
// vanilla OpenAI, Azure (with custom BaseURL), LiteLLM proxies, or
// any other server that speaks the /v1/embeddings shape.
func NewOpenAI(cfg OpenAIConfig) (Embedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("embed: OpenAIConfig.APIKey required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small"
	}
	if cfg.Dims == 0 {
		cfg.Dims = 1536
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	return &openAIEmbedder{cfg: cfg}, nil
}

type openAIRequest struct {
	Input          string `json:"input"`
	Model          string `json:"model"`
	Dimensions     int    `json:"dimensions,omitempty"`
	EncodingFormat string `json:"encoding_format,omitempty"`
}

type openAIResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (e *openAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(openAIRequest{
		Input:          text,
		Model:          e.cfg.Model,
		Dimensions:     e.cfg.Dims,
		EncodingFormat: "float",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.cfg.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)

	resp, err := e.cfg.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("embed: %s: %s", resp.Status, truncate(raw, 200))
	}
	var parsed openAIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("embed: parse: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("embed: provider error: %s", parsed.Error.Message)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("embed: empty data array in response")
	}
	v := parsed.Data[0].Embedding
	if len(v) != e.cfg.Dims {
		return nil, fmt.Errorf("embed: provider returned %d dims, want %d (model=%s)",
			len(v), e.cfg.Dims, parsed.Model)
	}
	return v, nil
}

func (e *openAIEmbedder) Dim() int      { return e.cfg.Dims }
func (e *openAIEmbedder) Model() string { return e.cfg.Model }

// ─── Stub embedder (offline / dev / tests) ──────────────

// stubEmbedder produces a deterministic, normalised vector by:
//  1. lower-casing + tokenising text on whitespace,
//  2. hashing each token to (dim, value) pairs and accumulating,
//  3. L2-normalising the result.
//
// It's NOT a real semantic embedding — but two texts sharing tokens
// land closer in cosine space than two with disjoint tokens, which
// is enough for end-to-end pipeline tests of "store → embed → recall".
type stubEmbedder struct {
	dim int
}

// NewStub returns a deterministic Embedder. Useful for tests and for
// dev environments without an API key. Dim defaults to 1536 to match
// the pgvector column.
func NewStub(dim int) Embedder {
	if dim <= 0 {
		dim = 1536
	}
	return &stubEmbedder{dim: dim}
}

func (s *stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, s.dim)
	tokens := strings.Fields(strings.ToLower(text))
	for _, tok := range tokens {
		// Two hashes per token: one picks the dimension, another the
		// signed contribution. Spreads tokens across the vector
		// without a learned model.
		h := sha256.Sum256([]byte(tok))
		// Use 4-byte slices: bytes 0-3 → dim index, bytes 4-7 → magnitude.
		dimIdx := int(binary.BigEndian.Uint32(h[0:4])) % s.dim
		if dimIdx < 0 {
			dimIdx += s.dim
		}
		mag := float32(int32(binary.BigEndian.Uint32(h[4:8]))) / float32(1<<31)
		v[dimIdx] += mag
		// Spread to a second dim so single-token texts still distribute.
		dim2 := int(binary.BigEndian.Uint32(h[8:12])) % s.dim
		if dim2 < 0 {
			dim2 += s.dim
		}
		mag2 := float32(int32(binary.BigEndian.Uint32(h[12:16]))) / float32(1<<31)
		v[dim2] += mag2
	}
	// L2 normalise — pgvector cosine_ops works on any vectors but
	// normalised inputs make `<=>` stable across magnitudes.
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		// Empty / all-zero → return a deterministic non-zero vector
		// so cosine doesn't divide by zero downstream.
		v[0] = 1
		return v, nil
	}
	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}
	return v, nil
}

func (s *stubEmbedder) Dim() int      { return s.dim }
func (s *stubEmbedder) Model() string { return fmt.Sprintf("stub-%d", s.dim) }

// ─── helpers ────────────────────────────────────────────

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
