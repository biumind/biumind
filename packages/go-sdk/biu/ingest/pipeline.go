// Two-step CoT pipeline that turns ParsedDoc into a PageDraft using any Provider.
//
//	Step 1 — Analyze: LLM reads the source, returns Outline (title + sections + summary + tags).
//	Step 2 — Generate: LLM produces final page blocks following the outline.
//
// Both steps use llm.Provider so the same code runs in:
//
//	server-side Brain ingest worker  → model-relay provider
//	user-side biu CLI `biu ingest`   → AnthropicDirect / model-relay
//	headless evaluation harness      → any provider
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

// Pipeline holds the configuration for an ingestion run.
type Pipeline struct {
	Provider     llm.Provider
	Model        string
	MaxBlocks    int           // safety cap on generated blocks; 0 → 64
	MaxBodyChars int           // truncate giant sources for prompts; 0 → 24000
	Timeout      time.Duration // per LLM call; 0 → 90s
}

// NewPipeline constructs a Pipeline with sensible defaults.
func NewPipeline(p llm.Provider, model string) *Pipeline {
	return &Pipeline{
		Provider:     p,
		Model:        model,
		MaxBlocks:    64,
		MaxBodyChars: 24000,
		Timeout:      90 * time.Second,
	}
}

// Ingest runs Parse → Step1 → Step2 and returns a ready-to-write PageDraft.
func (p *Pipeline) Ingest(ctx context.Context, src Source) (*PageDraft, error) {
	doc, err := Parse(src)
	if err != nil {
		return nil, fmt.Errorf("ingest: parse: %w", err)
	}
	body := flatten(doc, p.bodyCap())

	outline, err := p.step1Analyze(ctx, doc.Title, body, src.URL)
	if err != nil {
		return nil, fmt.Errorf("ingest: step1: %w", err)
	}
	blocks, err := p.step2Generate(ctx, outline, body)
	if err != nil {
		return nil, fmt.Errorf("ingest: step2: %w", err)
	}

	return &PageDraft{
		Title: outline.Title,
		Frontmatter: map[string]any{
			"summary":     outline.Summary,
			"tags":        outline.Tags,
			"source_url":  src.URL,
			"ingested_at": time.Now().UTC().Format(time.RFC3339),
		},
		Blocks:      blocks,
		Outline:     *outline,
		SourceURL:   src.URL,
		GeneratedAt: time.Now().UTC(),
	}, nil
}

// ─── Step 1: Analyze ────────────────────────────────────

const step1System = `You are an expert technical editor. You will be given a raw document and must return a JSON outline.

Return EXACTLY a JSON object with these fields and nothing else:
{
  "title":    "concise descriptive title (≤ 80 chars)",
  "summary":  "2-3 sentence summary",
  "tags":     ["short","topical","keywords"],
  "sections": [
    {"heading":"Section name","goal":"what this section will cover"}
  ]
}

Rules:
- Pick 2–8 sections. Each heading must be unique.
- Use the document's language (English / Chinese / etc).
- Output strict JSON: no prose, no markdown fences.`

func (p *Pipeline) step1Analyze(ctx context.Context, fallbackTitle, body, sourceURL string) (*Outline, error) {
	user := fmt.Sprintf("Source URL: %s\n\n--- BEGIN DOCUMENT ---\n%s\n--- END DOCUMENT ---", sourceURL, body)
	out, err := p.callJSON(ctx, step1System, user)
	if err != nil {
		return nil, err
	}
	var o Outline
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		return nil, fmt.Errorf("parse outline JSON: %w (raw: %.200s)", err, out)
	}
	if o.Title == "" {
		o.Title = fallbackTitle
	}
	if len(o.Sections) == 0 {
		// Fallback: synthesize one section from summary
		o.Sections = []OutlineEntry{{Heading: "Overview", Goal: o.Summary}}
	}
	return &o, nil
}

// ─── Step 2: Generate blocks ────────────────────────────

const step2System = `You are an expert technical writer. Produce a Wiki page following the given outline.

Return EXACTLY a JSON array of blocks. Each block must be one of:
  {"type":"heading","content":{"text":"Section name","level":2}}
  {"type":"text","content":{"text":"Paragraph body in markdown."}}
  {"type":"list","content":{"items":["item 1","item 2"]}}
  {"type":"code","content":{"language":"go","code":"…"}}

Rules:
- Start each section with a heading block (level 2).
- Inside a section, use text / list / code blocks as appropriate.
- Strict JSON array — no prose, no markdown fence.
- Do not invent facts. If the source is silent on a section's goal, write
  "(not covered in the source)".`

func (p *Pipeline) step2Generate(ctx context.Context, o *Outline, body string) ([]BlockDraft, error) {
	sections, _ := json.Marshal(o.Sections)
	user := fmt.Sprintf("OUTLINE: %s\n\nDOCUMENT TITLE: %s\nSUMMARY: %s\n\n--- DOCUMENT ---\n%s\n--- END DOCUMENT ---",
		string(sections), o.Title, o.Summary, body)
	raw, err := p.callJSON(ctx, step2System, user)
	if err != nil {
		return nil, err
	}
	var rawBlocks []struct {
		Type    string         `json:"type"`
		Content map[string]any `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &rawBlocks); err != nil {
		return nil, fmt.Errorf("parse blocks JSON: %w (raw: %.200s)", err, raw)
	}
	cap := p.blocksCap()
	if len(rawBlocks) > cap {
		rawBlocks = rawBlocks[:cap]
	}
	blocks := make([]BlockDraft, 0, len(rawBlocks))
	for i, b := range rawBlocks {
		t := strings.ToLower(strings.TrimSpace(b.Type))
		if t == "" {
			t = "text"
		}
		blocks = append(blocks, BlockDraft{
			Type:     t,
			Position: float64(i + 1),
			Content:  b.Content,
		})
	}
	return blocks, nil
}

// ─── Helpers ────────────────────────────────────────────

// callJSON sends a one-shot system+user prompt and collects the full response.
// Strips common markdown fences if the model adds them.
func (p *Pipeline) callJSON(ctx context.Context, system, user string) (string, error) {
	if p.Provider == nil {
		return "", errors.New("ingest: no provider configured")
	}
	if p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	model := p.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	frames, err := p.Provider.ChatStream(ctx, llm.ChatRequest{
		Model:     model,
		System:    system,
		Messages:  []llm.Message{{Role: "user", Content: user}},
		MaxTokens: 4096,
	})
	if err != nil {
		return "", err
	}
	text, err := llm.CollectText(frames)
	if err != nil {
		return "", err
	}
	return stripFence(text), nil
}

// stripFence removes ```json … ``` wrappers if the model added them.
func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	t = strings.TrimPrefix(t, "```json")
	t = strings.TrimPrefix(t, "```")
	t = strings.TrimSuffix(t, "```")
	return strings.TrimSpace(t)
}

func flatten(doc *ParsedDoc, max int) string {
	var sb strings.Builder
	if doc.Title != "" {
		sb.WriteString("# " + doc.Title + "\n\n")
	}
	for _, c := range doc.Chunks {
		if c.Heading != "" {
			sb.WriteString("## " + c.Heading + "\n\n")
		}
		sb.WriteString(c.Text)
		sb.WriteString("\n\n")
		if max > 0 && sb.Len() >= max {
			sb.WriteString("\n[…truncated…]")
			break
		}
	}
	return sb.String()
}

func (p *Pipeline) bodyCap() int {
	if p.MaxBodyChars > 0 {
		return p.MaxBodyChars
	}
	return 24000
}
func (p *Pipeline) blocksCap() int {
	if p.MaxBlocks > 0 {
		return p.MaxBlocks
	}
	return 64
}
