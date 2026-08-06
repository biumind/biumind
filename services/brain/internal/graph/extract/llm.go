// LLM-driven NER — augments the heuristic extractor with a Provider
// call that turns free-form prose into structured (nodes, relations).
//
// Combination strategy:
//  1. Run heuristics first (cheap, deterministic; #tag / @mention / wikilink).
//  2. Call the LLM with a prompt that ASKS for entities + relations
//     and the same vocabulary the heuristic uses (kind ∈ tag | person |
//     concept | resource | event | place).
//  3. Merge: dedupe by (kind, lower(name)). Heuristic wins on weight
//     ties so we don't downgrade high-confidence matches.
//
// Behind a feature flag (`GRAPH_LLM_NER=1`) — the LLM call is slow +
// costly + sometimes wrong; default off until we have a budget story.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

// LLMRelation is a typed edge candidate the LLM emitted.
type LLMRelation struct {
	Src      string  `json:"src"` // names match Candidate.Name
	Dst      string  `json:"dst"`
	Relation string  `json:"relation"` // mentions | links_to | related_to | references | parent_of
	Weight   float32 `json:"weight,omitempty"`
}

// LLMResult is everything one extraction round returned. Empty when the
// model couldn't find anything (or refused) — the caller must still
// surface the heuristic candidates.
type LLMResult struct {
	Candidates []Candidate
	Relations  []LLMRelation
}

const llmSystemPrompt = `You are a knowledge-graph extractor. Read the user's note and emit a JSON object with two arrays:

{
  "nodes": [{"kind": "tag|person|concept|resource|event|place", "name": "string", "summary": "≤120 chars"}],
  "relations": [{"src": "node-name", "dst": "node-name", "relation": "mentions|links_to|related_to|references|parent_of", "weight": 0.0..1.0}]
}

Rules:
- 'name' is the canonical surface form (lower-case for tag/person; preserve case for concept/place/event).
- Output ONLY valid JSON. No prose, no markdown, no commentary.
- If nothing notable, return {"nodes":[], "relations":[]}.
`

// FromTextWithLLM runs the heuristic extractor and merges with an LLM
// pass. `model` may be empty to use the provider's default. Errors from
// the provider are NOT fatal — we fall back to heuristic-only output so
// the pipeline keeps making progress.
func FromTextWithLLM(ctx context.Context, p llm.Provider, model, text string) (LLMResult, error) {
	heur := FromText(text)
	out := LLMResult{Candidates: heur}
	if p == nil || strings.TrimSpace(text) == "" {
		return out, nil
	}

	frames, err := p.ChatStream(ctx, llm.ChatRequest{
		Model:     model,
		System:    llmSystemPrompt,
		Messages:  []llm.Message{{Role: "user", Content: text}},
		MaxTokens: 1024,
	})
	if err != nil {
		return out, fmt.Errorf("llm-ner: stream: %w", err)
	}
	body, err := llm.CollectText(frames)
	if err != nil {
		return out, fmt.Errorf("llm-ner: collect: %w", err)
	}
	body = stripCodeFence(body)

	var parsed struct {
		Nodes []struct {
			Kind    string `json:"kind"`
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"nodes"`
		Relations []LLMRelation `json:"relations"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return out, fmt.Errorf("llm-ner: parse: %w (body=%q)", err, body)
	}

	// Merge nodes into Candidates with dedupe. Heuristic items already
	// captured are skipped (they're 90% confidence — don't downgrade).
	seen := make(map[string]bool, len(out.Candidates))
	for _, c := range out.Candidates {
		seen[c.Kind+"\x00"+strings.ToLower(c.Name)] = true
	}
	for _, n := range parsed.Nodes {
		kind := strings.ToLower(strings.TrimSpace(n.Kind))
		name := strings.TrimSpace(n.Name)
		if name == "" {
			continue
		}
		if !validKind(kind) {
			continue
		}
		key := kind + "\x00" + strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Candidates = append(out.Candidates, Candidate{
			Kind:     kind,
			Name:     name,
			Original: name,
			Relation: "mentions",
			Weight:   0.7, // LLM confidence floor; heuristics are 0.8–1.0
		})
	}
	out.Relations = parsed.Relations
	return out, nil
}

// stripCodeFence — models love wrapping JSON in ```json ... ```.
// Best-effort unwrap so we accept either form.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence (and optional language tag) up to first newline.
	if nl := strings.IndexByte(s, '\n'); nl > 0 {
		s = s[nl+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func validKind(k string) bool {
	switch k {
	case "tag", "person", "concept", "resource", "event", "place":
		return true
	}
	return false
}
