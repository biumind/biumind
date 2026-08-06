// LLM-driven precision filter for dedup candidates.
//
// Cosine ≥ 0.92 is good recall but mediocre precision: same-topic
// pages with different angles (e.g. "RoPE — math derivation" vs
// "RoPE — implementation") routinely cross the threshold without
// being true merge candidates. Without LLM filtering, the review
// queue trains the user to ignore dedup entirely.
//
// This filter calls model-relay /v1/messages once per scan with all candidate
// pairs in a single batched prompt, asks the model to classify each
// as "duplicate" (genuinely the same topic, should merge) vs
// "related" (similar enough to surface in retrieval, should stay
// separate), and drops "related" pairs before they hit review_items.
//
// Failure modes:
//   - model-relay down / LLM error → all pairs pass through (recall over precision;
//     a noisy queue is better than missed duplicates)
//   - bad JSON output → same passthrough behavior; we log and move on
//   - per-pair verdict missing → that pair is kept (treat as "duplicate"
//     to avoid losing real findings to malformed output)
package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

// LLMFilter is the optional dependency dedup / lint / sweep workers
// use to improve precision. Implementations are stateless / safe for
// concurrent use; the worker shares one instance across ticks.
type LLMFilter interface {
	// FilterDedup returns the subset of `pairs` the model classified as
	// genuine duplicates. Order is preserved. Implementations MUST NOT
	// fail the entire batch on a single-pair error — the worker treats
	// a returned error as "filter unavailable" and writes all pairs.
	FilterDedup(ctx context.Context, ownerID uuid.UUID, pairs []PagePair) ([]PagePair, error)

	// FilterFindings prunes auto-detected lint / sweep findings to the
	// subset the model thinks is actually actionable. Same recall-over-
	// precision contract: errors → return input unchanged so a model-relay
	// outage doesn't drop findings.
	//
	// Implementations may skip the LLM call for rules that are 100%
	// deterministic (empty_page / untitled_page / dead_wikilink /
	// stale_page) — those don't benefit from a second opinion. The
	// caller doesn't need to know which rules get filtered: it just
	// hands all findings over and trusts the implementation to
	// preserve deterministic ones.
	FilterFindings(ctx context.Context, ownerID uuid.UUID, findings []Finding) ([]Finding, error)
}

// NoopFilter passes every input through unchanged. Used when no model-relay /
// JWT signer is configured — keeps worker behaviour identical to
// pre-P2-D-LLM (rule-only).
type NoopFilter struct{}

func (NoopFilter) FilterDedup(_ context.Context, _ uuid.UUID, pairs []PagePair) ([]PagePair, error) {
	return pairs, nil
}

func (NoopFilter) FilterFindings(_ context.Context, _ uuid.UUID, findings []Finding) ([]Finding, error) {
	return findings, nil
}

// HubLLMFilter calls biumind model-relay on behalf of `ownerID` (so the
// request resolves to that user's BYOK / platform-pool credentials)
// using a short-lived JWT minted from the Signer.
type HubLLMFilter struct {
	RelayURL string
	Model    string
	Signer   *bauth.Signer
	HTTP     *http.Client
	Timeout  time.Duration
	Logger   *slog.Logger
	// MaxPairsPerCall caps the prompt size; larger batches lose
	// reliability as the model loses track of which verdict belongs
	// to which pair. 20 was empirically the sweet spot in dogfood.
	MaxPairsPerCall int
	// SnippetMaxChars caps each page snippet so a 50KB block doesn't
	// dominate the prompt. The model only needs enough context to
	// recognise topical overlap, not to reproduce the page.
	SnippetMaxChars int
}

func NewHubLLMFilter(relayURL, model string, signer *bauth.Signer, logger *slog.Logger) *HubLLMFilter {
	return &HubLLMFilter{
		RelayURL: strings.TrimRight(relayURL, "/"),
		Model:    model,
		Signer:   signer,
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
		},
		Timeout:         45 * time.Second,
		Logger:          logger,
		MaxPairsPerCall: 20,
		SnippetMaxChars: 400,
	}
}

func (h *HubLLMFilter) FilterDedup(ctx context.Context, ownerID uuid.UUID, pairs []PagePair) ([]PagePair, error) {
	if len(pairs) == 0 {
		return pairs, nil
	}
	if h.RelayURL == "" || h.Signer == nil {
		// Misconfigured — degrade to passthrough, not failure.
		return pairs, nil
	}

	// Batch in chunks. Within each chunk, pair index is stable so the
	// returned verdict map keys back to the input slice unambiguously.
	out := make([]PagePair, 0, len(pairs))
	for start := 0; start < len(pairs); start += h.MaxPairsPerCall {
		end := start + h.MaxPairsPerCall
		if end > len(pairs) {
			end = len(pairs)
		}
		batch := pairs[start:end]
		verdicts, err := h.classifyBatch(ctx, ownerID, batch)
		if err != nil {
			// Log + keep this batch as-is. The whole point of the
			// recall-fallback contract: never lose findings to LLM flakiness.
			if h.Logger != nil {
				h.Logger.Warn("dedup llm filter: batch failed, keeping all",
					"start", start, "size", len(batch), "err", err)
			}
			out = append(out, batch...)
			continue
		}
		for i, p := range batch {
			v, ok := verdicts[i]
			if !ok {
				// Missing verdict — treat as duplicate (recall side).
				out = append(out, p)
				continue
			}
			if v == "duplicate" {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// ─── internals ─────────────────────────────────────────────────

// FilterFindings prunes lint / sweep findings to those the LLM
// considers genuinely actionable. Deterministic rules pass through
// unchanged; only judgement-call rules (stub_page, orphaned_page)
// are routed through the model.
//
// Why hard-code the rule set instead of asking the LLM about every
// rule: empty/untitled/dead-wikilink/stale verdicts are mechanically
// derivable — there's no signal to extract from a second opinion,
// only noise + cost. stub vs intentional-reference and orphan vs
// "linked under an alias" need contextual judgement; the LLM helps.
func (h *HubLLMFilter) FilterFindings(ctx context.Context, ownerID uuid.UUID, findings []Finding) ([]Finding, error) {
	if len(findings) == 0 {
		return findings, nil
	}
	if h.RelayURL == "" || h.Signer == nil {
		return findings, nil
	}

	// Partition: pass-through for deterministic rules, LLM-classify
	// for judgement-call rules. Order is preserved across the merge.
	type slot struct {
		idx int // position in original `findings`
		f   Finding
		llm bool // true if this slot needs LLM verdict
	}
	slots := make([]slot, 0, len(findings))
	llmIdx := 0
	for i, f := range findings {
		needsLLM := f.RuleID == RuleStubPage || f.RuleID == RuleOrphanedPage
		slots = append(slots, slot{idx: i, f: f, llm: needsLLM})
		if needsLLM {
			llmIdx++
		}
	}
	if llmIdx == 0 {
		return findings, nil
	}

	// Build the LLM batch + map back to slot index after.
	batch := make([]Finding, 0, llmIdx)
	batchOrigin := make([]int, 0, llmIdx)
	for i, s := range slots {
		if s.llm {
			batchOrigin = append(batchOrigin, i)
			batch = append(batch, s.f)
		}
	}
	verdicts, err := h.classifyFindingsBatch(ctx, ownerID, batch)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("findings llm filter: errored, keeping all",
				"size", len(batch), "err", err)
		}
		// Recall fallback — return original ordering unmodified.
		return findings, nil
	}

	out := make([]Finding, 0, len(findings))
	for i, s := range slots {
		if !s.llm {
			out = append(out, s.f)
			continue
		}
		// Find this slot's position within the LLM batch.
		batchPos := -1
		for j, oi := range batchOrigin {
			if oi == i {
				batchPos = j
				break
			}
		}
		v, ok := verdicts[batchPos]
		if !ok {
			// Missing verdict → keep (recall side; same as dedup path).
			out = append(out, s.f)
			continue
		}
		if v == "actionable" {
			out = append(out, s.f)
		}
		// "skip" verdict → drop
	}
	return out, nil
}

// classifyFindingsBatch sends one prompt asking the model to label
// each finding as "actionable" (real issue worth fixing) vs "skip"
// (probably a false positive — intentional short reference page,
// orphan that's actually linked via an alias the worker missed).
func (h *HubLLMFilter) classifyFindingsBatch(ctx context.Context, ownerID uuid.UUID, batch []Finding) (map[int]string, error) {
	prompt := h.buildFindingsPrompt(batch)

	jwt, err := h.Signer.Sign(&bauth.Claims{UserID: ownerID.String()})
	if err != nil {
		return nil, fmt.Errorf("mint jwt: %w", err)
	}
	body := map[string]any{
		"model":      h.Model,
		"stream":     false,
		"max_tokens": 4096,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	cctx := ctx
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		h.RelayURL+"/v1/messages", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("model-relay %d: %s", resp.StatusCode, string(buf))
	}
	var hubResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hubResp); err != nil {
		return nil, fmt.Errorf("decode Relay: %w", err)
	}
	if len(hubResp.Choices) == 0 {
		return nil, fmt.Errorf("model-relay returned no choices")
	}
	return parseFindingVerdicts(hubResp.Choices[0].Message.Content)
}

func (h *HubLLMFilter) buildFindingsPrompt(batch []Finding) string {
	var b strings.Builder
	b.WriteString(
		"You are auditing a wiki maintenance bot's automated findings.\n" +
			"For each finding below, decide whether it is ACTIONABLE (a real " +
			"issue the user should fix) or SKIP (a false positive — e.g. an " +
			"intentionally short reference page, or an 'orphan' that's " +
			"actually linked elsewhere via aliases).\n\n" +
			"Output STRICT JSON only — no markdown, no preamble. Schema:\n" +
			"  [{\"id\": <int>, \"verdict\": \"actionable\"|\"skip\", \"reason\": \"<short>\"}]\n\n" +
			"Findings:\n",
	)
	for i, f := range batch {
		fmt.Fprintf(&b, "\n--- Finding %d ---\nrule: %s\ntitle: %s\ndescription: %s\n",
			i, f.RuleID, f.Title, f.Description)
		if v, ok := f.Payload["block_count"]; ok {
			fmt.Fprintf(&b, "blocks: %v\n", v)
		}
		if v, ok := f.Payload["char_count"]; ok {
			fmt.Fprintf(&b, "chars: %v\n", v)
		}
		if v, ok := f.Payload["days_idle"]; ok {
			fmt.Fprintf(&b, "days_idle: %v\n", v)
		}
	}
	return b.String()
}

func parseFindingVerdicts(content string) (map[int]string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty model output")
	}
	if strings.HasPrefix(content, "```") {
		if nl := strings.IndexByte(content, '\n'); nl > 0 {
			content = content[nl+1:]
		}
		if idx := strings.LastIndex(content, "```"); idx >= 0 {
			content = content[:idx]
		}
	}
	start := strings.IndexByte(content, '[')
	end := strings.LastIndexByte(content, ']')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in output")
	}
	jsonText := content[start : end+1]
	type entry struct {
		ID      any    `json:"id"`
		Verdict string `json:"verdict"`
	}
	var entries []entry
	if err := json.Unmarshal([]byte(jsonText), &entries); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	out := make(map[int]string, len(entries))
	for _, e := range entries {
		idx, ok := coerceIntID(e.ID)
		if !ok {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(e.Verdict))
		switch v {
		case "actionable", "skip":
			out[idx] = v
		}
	}
	return out, nil
}

// classifyBatch builds a single LLM prompt for `batch`, parses the
// returned JSON, and returns map[index]verdict where verdict is
// "duplicate" or "related". Missing indices in the result imply the
// model didn't emit a verdict for that pair — the caller treats them
// as "duplicate" to preserve recall.
func (h *HubLLMFilter) classifyBatch(ctx context.Context, ownerID uuid.UUID, batch []PagePair) (map[int]string, error) {
	prompt := h.buildPrompt(batch)

	// Mint a short-lived JWT impersonating ownerID. model-relay's CredsResolver
	// reads the user_id from the token claims and resolves their BYOK
	// or platform-pool credentials normally.
	jwt, err := h.Signer.Sign(&bauth.Claims{UserID: ownerID.String()})
	if err != nil {
		return nil, fmt.Errorf("mint jwt: %w", err)
	}

	body := map[string]any{
		"model":      h.Model,
		"stream":     false,
		"max_tokens": 4096,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	cctx := ctx
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		h.RelayURL+"/v1/messages", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Surface a truncated upstream body so misconfigured deploys
		// (no model-relay creds, no provider configured for ownerID, etc.)
		// produce actionable logs rather than mysterious "kept all".
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("model-relay %d: %s", resp.StatusCode, string(buf))
	}

	var hubResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hubResp); err != nil {
		return nil, fmt.Errorf("decode Relay: %w", err)
	}
	if len(hubResp.Choices) == 0 {
		return nil, fmt.Errorf("model-relay returned no choices")
	}
	return parseVerdicts(hubResp.Choices[0].Message.Content)
}

// buildPrompt renders the batched classification prompt. The schema is
// stable so a future move to function-calling / structured output is
// drop-in (we'd just change the response parser).
func (h *HubLLMFilter) buildPrompt(batch []PagePair) string {
	cap := h.SnippetMaxChars
	if cap <= 0 {
		cap = 400
	}
	var b strings.Builder
	b.WriteString(
		"You are auditing wiki page pairs for duplication.\n" +
			"Each pair below has cosine similarity ≥ 0.92, meaning the " +
			"embedding model thinks they are highly related. Your job is " +
			"to decide whether they are TRUE duplicates that should be " +
			"merged into one canonical page, or merely RELATED pages " +
			"that should stay separate.\n\n" +
			"Examples:\n" +
			"  duplicate — Two pages that cover the same entity / concept, " +
			"likely from different ingest sessions on the same source.\n" +
			"  related   — Pages on a shared topic but with distinct " +
			"angles (e.g. theory vs implementation, overview vs deep-dive).\n\n" +
			"Output STRICT JSON only — no markdown, no preamble. Schema:\n" +
			"  [{\"id\": <int>, \"verdict\": \"duplicate\"|\"related\", \"reason\": \"<short>\"}]\n\n" +
			"Pairs:\n",
	)
	for i, p := range batch {
		fmt.Fprintf(&b, "\n--- Pair %d ---\n", i)
		fmt.Fprintf(&b, "A: %s\n%s\n", p.TitleA, truncate(p.SnippetA, cap))
		fmt.Fprintf(&b, "B: %s\n%s\n", p.TitleB, truncate(p.SnippetB, cap))
	}
	return b.String()
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// parseVerdicts pulls the first JSON array out of `content` and returns
// map[id]verdict. We accept the model wrapping its output in markdown
// fences or prose — we just look for the first `[` and last `]` that
// balances. Unbalanced / unparseable content returns an error so the
// caller falls back to "keep all".
func parseVerdicts(content string) (map[int]string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty model output")
	}
	// Strip optional leading "```json" / trailing "```".
	if strings.HasPrefix(content, "```") {
		// Find first newline after the opening fence and trim.
		if nl := strings.IndexByte(content, '\n'); nl > 0 {
			content = content[nl+1:]
		}
		if idx := strings.LastIndex(content, "```"); idx >= 0 {
			content = content[:idx]
		}
	}
	// Locate the first '[' and pair it with the last ']' that yields
	// a parseable array. Loose recovery — the model might emit
	// trailing commentary even after we ask it not to.
	start := strings.IndexByte(content, '[')
	end := strings.LastIndexByte(content, ']')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in output")
	}
	jsonText := content[start : end+1]

	type entry struct {
		ID      any    `json:"id"` // int or string in practice
		Verdict string `json:"verdict"`
	}
	var entries []entry
	if err := json.Unmarshal([]byte(jsonText), &entries); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	out := make(map[int]string, len(entries))
	for _, e := range entries {
		idx, ok := coerceIntID(e.ID)
		if !ok {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(e.Verdict))
		switch v {
		case "duplicate", "related":
			out[idx] = v
		}
	}
	return out, nil
}

// coerceIntID accepts both numeric and string-form IDs the model might
// emit (Anthropic models tend to be precise; OpenAI sometimes wraps
// ints in strings). Anything unparseable is silently dropped — better
// to lose one verdict (keep that pair) than fail the batch.
func coerceIntID(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		var n int
		if _, err := fmt.Sscanf(x, "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}
