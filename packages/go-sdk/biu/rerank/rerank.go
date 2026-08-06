// Package rerank defines the canonical relevance-rerank contract used
// across BiuMind services.
//
// Today there are two implementations:
//
//	CohereReranker — calls /rerank on Cohere-shape APIs (Cohere,
//	                 SiliconFlow, Jina, model-relay /v1/rerank, etc.)
//	StubReranker   — deterministic token-overlap scoring for offline
//	                 tests and dev environments without an API key.
//
// Rerank sits after retrieval+RRF fusion and before feedback rerank:
//
//	recall → RRF fuse → cross-encoder rerank (this) → feedback rerank
//
// Brain.Search's handler picks the reranker by env at boot:
//
//	RERANK_PROVIDER=cohere  RERANK_API_KEY=…  RERANK_MODEL=BAAI/bge-reranker-v2-m3
//	RERANK_PROVIDER=stub    (default; no network)
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Score is one document's rerank verdict: Index into the input docs
// slice and the calibrated relevance score in [0,1].
type Score struct {
	Index int     `json:"index"`
	Score float64 `json:"relevance_score"`
}

// Reranker is the contract every relevance-rerank backend implements.
type Reranker interface {
	// Rerank scores `docs` against `query` and returns one Score per
	// doc, sorted by relevance descending. topN<=0 or >=len(docs)
	// returns the full ranking; otherwise the top topN only.
	Rerank(ctx context.Context, query string, docs []string, topN int) ([]Score, error)
	// Model is a free-form identifier (e.g. "bge-reranker-v2-m3",
	// "stub-overlap"). Logged for traceability.
	Model() string
}

// ─── Cohere-shape HTTP reranker ────────────────────────

// CohereConfig configures a Cohere-shape reranker. Works against Cohere
// proper, SiliconFlow, Jina, or model-relay's /v1/rerank (which speaks
// the same Cohere transport).
type CohereConfig struct {
	BaseURL string // default https://api.cohere.ai/v1 ; POST {BaseURL}/rerank
	APIKey  string // required
	Model   string // default BAAI/bge-reranker-v2-m3
	HTTP    *http.Client
}

type cohereReranker struct {
	cfg CohereConfig
}

// NewCohere constructs a Cohere-shape reranker. model-relay is the
// canonical egress (I6): pass its /v1 root as BaseURL and the internal
// token as APIKey when calling server-side.
func NewCohere(cfg CohereConfig) (Reranker, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("rerank: CohereConfig.APIKey required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.cohere.ai/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Model == "" {
		cfg.Model = "BAAI/bge-reranker-v2-m3"
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	return &cohereReranker{cfg: cfg}, nil
}

type cohereRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents"`
}

type cohereResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
	ID    string `json:"id"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (r *cohereReranker) Rerank(ctx context.Context, query string, docs []string, topN int) ([]Score, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	reqBody := cohereRequest{
		Model:           r.cfg.Model,
		Query:           query,
		Documents:       docs,
		ReturnDocuments: false, // we only need index + score, skip doc text echo
	}
	if topN > 0 && topN < len(docs) {
		reqBody.TopN = topN
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.cfg.BaseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)

	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("rerank: %s: %s", resp.Status, truncate(raw, 200))
	}
	var parsed cohereResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("rerank: parse: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("rerank: provider error: %s", parsed.Error.Message)
	}
	out := make([]Score, 0, len(parsed.Results))
	for _, res := range parsed.Results {
		out = append(out, Score{Index: res.Index, Score: res.RelevanceScore})
	}
	// Providers return results sorted desc already, but re-sort to be
	// safe so downstream never depends on provider ordering.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func (r *cohereReranker) Model() string { return r.cfg.Model }

// ─── Stub reranker (offline / dev / tests) ────────────

// stubReranker scores docs by token overlap with the query:
//
//  1. lower-case + tokenise query and each doc on whitespace,
//  2. score = |query_tokens ∩ doc_tokens| / |query_tokens ∪ doc_tokens|
//     (Jaccard),  0 when either side is empty,
//  3. stable-sort docs by score descending.
//
// It's NOT a real semantic rerank — but docs sharing more query terms
// score higher, which is enough for end-to-end pipeline tests of
// "recall → fuse → rerank" without a real model.
type stubReranker struct{}

// NewStub returns a deterministic Reranker. Useful for tests and for
// dev environments without an API key.
func NewStub() Reranker { return &stubReranker{} }

func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, t := range strings.Fields(strings.ToLower(s)) {
		out[t] = struct{}{}
	}
	return out
}

func (s *stubReranker) Rerank(_ context.Context, query string, docs []string, topN int) ([]Score, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	qt := tokenSet(query)
	scores := make([]Score, len(docs))
	for i, d := range docs {
		dt := tokenSet(d)
		var inter, union int
		for tok := range qt {
			union++
			if _, ok := dt[tok]; ok {
				inter++
			}
		}
		for tok := range dt {
			if _, ok := qt[tok]; !ok {
				union++
			}
		}
		jaccard := 0.0
		if union > 0 {
			jaccard = float64(inter) / float64(union)
		}
		scores[i] = Score{Index: i, Score: jaccard}
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })
	if topN > 0 && topN < len(scores) {
		scores = scores[:topN]
	}
	return scores, nil
}

func (s *stubReranker) Model() string { return "stub-overlap" }

// ─── helpers ──────────────────────────────────────────

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
