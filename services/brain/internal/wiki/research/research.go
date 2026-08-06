// Deep Research orchestrator.
//
// Pipeline:
//
//  1. Web search across the user's queries (or the topic itself)
//  2. Build wiki index from existing project page titles for cross-ref
//  3. LLM synthesises results into a markdown wiki page
//  4. Strip <think> blocks
//  5. Save as new page in the project — title becomes
//     "Research: <topic>", body lives as a single markdown block
//     (the standard chunker / embedworker / enrich worker pick it
//     up from there).
//
// The orchestrator runs in its own goroutine. The HTTP handler
// returns immediately with the task id; the client polls
// /v1/wiki/projects/{id}/research/{taskId} for status until done.
package research

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/biumind/biumind/services/brain/internal/search/searxng"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LLMCaller is the same minimal interface enrich uses. We don't import
// it across packages to keep test wiring simple.
type LLMCaller interface {
	Chat(ctx context.Context, ownerID uuid.UUID, system, user string) (string, error)
}

// Orchestrator runs deep research tasks end-to-end.
type Orchestrator struct {
	pool          *pgxpool.Pool
	store         *Store
	wiki          *wikistore.Store
	searx         *searxng.Client
	llm           LLMCaller
	logger        *slog.Logger
	maxResults    int
	llmTimeout    time.Duration
	maxConcurrent int
	// sem bounds how many research pipelines run at once — across both
	// HTTP-spawned tasks and boot-recovered ones. Buffered channel of
	// maxConcurrent slots; acquire on entry, release on exit.
	sem chan struct{}
}

type Config struct {
	MaxResultsPerQuery int
	LLMTimeout         time.Duration
	// MaxConcurrent caps in-flight research pipelines. 0 → 4. Each
	// pipeline holds an LLM call (30-90s) + N web searches, so this is
	// the primary knob for protecting model-relay / SearxNG throughput.
	MaxConcurrent int
	Logger        *slog.Logger
}

func NewOrchestrator(pool *pgxpool.Pool, s *Store, w *wikistore.Store,
	searx *searxng.Client, llm LLMCaller, cfg Config) *Orchestrator {
	if cfg.MaxResultsPerQuery == 0 {
		cfg.MaxResultsPerQuery = 5
	}
	if cfg.LLMTimeout == 0 {
		cfg.LLMTimeout = 90 * time.Second
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 4
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		pool: pool, store: s, wiki: w, searx: searx, llm: llm,
		maxResults:    cfg.MaxResultsPerQuery,
		llmTimeout:    cfg.LLMTimeout,
		maxConcurrent: cfg.MaxConcurrent,
		sem:           make(chan struct{}, cfg.MaxConcurrent),
		logger:        logger,
	}
}

// Run executes the full pipeline for `taskID`. It updates the task
// status as it progresses and writes the final page id (or error
// message) when done. Designed to be called as `go orch.Run(...)`.
//
// Run is the single concurrency chokepoint: it acquires a slot on the
// bounded sem before doing work, so neither HTTP-spawned tasks nor
// boot-recovered ones can exceed MaxConcurrent. It is also safe to call
// for a task that a previous (crashed) run already partly completed —
// run() resumes from whatever phase the persisted data indicates, and
// savePage reuses an existing page instead of duplicating it.
func (o *Orchestrator) Run(ctx context.Context, taskID uuid.UUID) {
	// Block until a concurrency slot opens. Recovered tasks and fresh
	// HTTP tasks share the same pool, so a boot recover of 20 stuck
	// tasks can't starve out everything else.
	select {
	case o.sem <- struct{}{}:
		defer func() { <-o.sem }()
	case <-ctx.Done():
		o.logger.Warn("research: cancelled waiting for slot", "id", taskID, "err", ctx.Err())
		return
	}

	task, err := o.store.Get(ctx, taskID)
	if err != nil {
		o.logger.Warn("research: load task", "id", taskID, "err", err)
		return
	}
	// Already terminal — a duplicate Run (or a recover that raced a
	// just-finished task) must not re-run and risk a second page.
	if task.Status == StatusDone || task.Status == StatusError {
		return
	}
	if err := o.run(ctx, task); err != nil {
		o.logger.Warn("research: failed", "id", taskID, "err", err)
		_ = o.store.Fail(ctx, taskID, err.Error())
	}
}

// resumePhase decides where run() re-enters for a (possibly partially
// completed) task. Pure function of the persisted task state, so it is
// unit-testable without a database.
//
//	page_id set       → already saved; nothing left
//	synthesis present → search+LLM done; just persist the page
//	web_results set   → search done; synthesise + save
//	else              → (re)search from scratch
func resumePhase(t *Task) phase {
	if t.PageID != nil {
		return phaseDone
	}
	if strings.TrimSpace(t.Synthesis) != "" {
		return phaseSave
	}
	if len(t.WebResults) > 0 {
		return phaseSynthesize
	}
	return phaseSearch
}

type phase int

const (
	phaseSearch phase = iota
	phaseSynthesize
	phaseSave
	phaseDone
)

func (o *Orchestrator) run(ctx context.Context, task *Task) error {
	switch resumePhase(task) {
	case phaseDone:
		// Page already exists from a prior run that crashed after
		// CreatePage but the recover picked the row up before status
		// flipped to done. Stamp it done and stop.
		return o.store.Complete(ctx, task.ID, *task.PageID)

	case phaseSave:
		return o.savePage(ctx, task, task.Synthesis, task.WebResults)

	case phaseSynthesize:
		if err := o.store.SetStatus(ctx, task.ID, StatusSynthesizing); err != nil {
			return err
		}
		wikiIndex, err := o.buildWikiIndex(ctx, task.ProjectID)
		if err != nil {
			o.logger.Warn("research: wiki index unavailable",
				"project_id", task.ProjectID, "err", err)
			wikiIndex = ""
		}
		synthesis, err := o.synthesize(ctx, task, task.WebResults, wikiIndex)
		if err != nil {
			return fmt.Errorf("synthesize: %w", err)
		}
		if err := o.store.AppendSynthesis(ctx, task.ID, synthesis); err != nil {
			return err
		}
		return o.savePage(ctx, task, synthesis, task.WebResults)
	}

	// phaseSearch — the full pipeline from the top.
	// 1. Web search.
	if err := o.store.SetStatus(ctx, task.ID, StatusSearching); err != nil {
		return err
	}
	hits, err := o.search(ctx, task)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if err := o.store.SaveWebResults(ctx, task.ID, hits); err != nil {
		return err
	}
	if len(hits) == 0 {
		// No web results — still create a placeholder page so the user
		// sees something instead of a silent error.
		return o.savePage(ctx, task, "No web results found for this topic.", nil)
	}

	// 2. Build wiki index for cross-referencing.
	wikiIndex, err := o.buildWikiIndex(ctx, task.ProjectID)
	if err != nil {
		o.logger.Warn("research: wiki index unavailable",
			"project_id", task.ProjectID, "err", err)
		wikiIndex = ""
	}

	// 3. LLM synthesis.
	if err := o.store.SetStatus(ctx, task.ID, StatusSynthesizing); err != nil {
		return err
	}
	synthesis, err := o.synthesize(ctx, task, hits, wikiIndex)
	if err != nil {
		return fmt.Errorf("synthesize: %w", err)
	}
	if err := o.store.AppendSynthesis(ctx, task.ID, synthesis); err != nil {
		return err
	}

	// 4. Save as a wiki page.
	return o.savePage(ctx, task, synthesis, hits)
}

func (o *Orchestrator) search(ctx context.Context, task *Task) ([]WebHit, error) {
	if o.searx == nil {
		return nil, errors.New("searxng not configured")
	}
	queries := task.Queries
	if len(queries) == 0 {
		queries = []string{task.Topic}
	}
	seen := map[string]struct{}{}
	out := make([]WebHit, 0, len(queries)*o.maxResults)
	for _, q := range queries {
		results, err := o.searx.Search(ctx, q, o.maxResults)
		if err != nil {
			// Per-query failure is non-fatal — keep the others.
			o.logger.Warn("research: query failed", "query", q, "err", err)
			continue
		}
		for _, r := range results {
			if _, dup := seen[r.URL]; dup {
				continue
			}
			seen[r.URL] = struct{}{}
			out = append(out, WebHit{
				Title:   r.Title,
				URL:     r.URL,
				Snippet: r.Snippet,
				Source:  r.Engine,
			})
		}
	}
	return out, nil
}

func (o *Orchestrator) buildWikiIndex(ctx context.Context, projectID uuid.UUID) (string, error) {
	pages, err := o.wiki.ListPages(ctx, projectID, 200)
	if err != nil {
		return "", err
	}
	var lines []string
	seen := map[string]struct{}{}
	for _, p := range pages {
		t := strings.TrimSpace(p.Title)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		lines = append(lines, "- "+t)
	}
	return strings.Join(lines, "\n"), nil
}

const baseSystemPrompt = `You are a research assistant. Synthesize the web search results into a comprehensive wiki page in markdown.

## Cross-referencing (IMPORTANT)
- The wiki may already contain pages listed in the Wiki Index below.
- When your synthesis mentions an entity or concept that exists in the wiki, ALWAYS use [[wikilink]] syntax to link to it.
- For example, if the wiki has an entity 'anthropic', write [[anthropic]] when mentioning it.
- This is critical for connecting new research to existing knowledge in the graph.

## Writing Rules
- Organize into clear sections with markdown headings (##, ###).
- Cite web sources using [N] notation matching the result list.
- Note contradictions or gaps explicitly.
- Suggest additional sources worth finding when relevant.
- Neutral, encyclopedic tone.
- Output a complete markdown body — NO preamble, NO explanation around the page.
`

func (o *Orchestrator) synthesize(ctx context.Context, task *Task, hits []WebHit, wikiIndex string) (string, error) {
	system := baseSystemPrompt
	if wikiIndex != "" {
		system += "\n## Existing Wiki Index (link to these pages with [[wikilink]])\n" + wikiIndex
	}
	var ctxLines []string
	for i, h := range hits {
		ctxLines = append(ctxLines, fmt.Sprintf("[%d] **%s** (%s)\n%s", i+1, h.Title, h.Source, h.Snippet))
	}
	user := fmt.Sprintf("Research topic: **%s**\n\n## Web Search Results\n\n%s\n\nSynthesize into a wiki page.",
		task.Topic, strings.Join(ctxLines, "\n\n"))

	llmCtx, cancel := context.WithTimeout(ctx, o.llmTimeout)
	defer cancel()
	raw, err := o.llm.Chat(llmCtx, task.OwnerID, system, user)
	if err != nil {
		return "", err
	}
	return cleanThinking(raw), nil
}

// thinkingRe strips both closed `<think>...</think>` and unclosed
// `<think>...` blocks from a model output. Some reasoning-mode models
// leak their scratch pad into the final answer otherwise.
var thinkingRe = regexp.MustCompile(`(?s)<think(?:ing)?>\s*.*?</think(?:ing)?>\s*`)
var thinkingOpenRe = regexp.MustCompile(`(?s)<think(?:ing)?>\s*.*$`)

func cleanThinking(s string) string {
	s = thinkingRe.ReplaceAllString(s, "")
	s = thinkingOpenRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func (o *Orchestrator) savePage(ctx context.Context, task *Task, synthesis string, hits []WebHit) error {
	if err := o.store.SetStatus(ctx, task.ID, StatusSaving); err != nil {
		return err
	}

	// Idempotency guard: a prior run may have created the page but
	// crashed before Complete stamped page_id (the CreatePage commit
	// and the task-row update are separate statements). Reuse the
	// existing page instead of writing a duplicate.
	if existing, err := o.store.FindPageByTaskID(ctx, task.ProjectID, task.ID); err != nil {
		return fmt.Errorf("lookup existing page: %w", err)
	} else if existing != nil {
		return o.store.Complete(ctx, task.ID, *existing)
	}

	title := "Research: " + task.Topic

	// Compose the body: synthesis + ## References from web hits.
	var body strings.Builder
	body.WriteString(synthesis)
	if len(hits) > 0 {
		body.WriteString("\n\n## References\n\n")
		for i, h := range hits {
			fmt.Fprintf(&body, "%d. [%s](%s) — %s\n", i+1, h.Title, h.URL, h.Source)
		}
	}

	page, err := o.wiki.CreatePage(ctx, wikistore.CreatePageInput{
		ProjectID: task.ProjectID,
		Title:     title,
		ActorID:   task.OwnerID.String(),
		// Provenance markers — UI can filter "research-origin" pages
		// from the regular list, and `kind:query` distinguishes
		// these from manually-authored pages in metrics. research_taskid
		// is also the idempotency key savePage re-looks-up on recover.
		Frontmatter: map[string]any{
			"origin":          "deep-research",
			"kind":            "query",
			"research_topic":  task.Topic,
			"research_taskid": task.ID.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("create page: %w", err)
	}
	// One markdown block holds the full body — chunker splits it
	// further at embed time. Single block keeps page edits simple.
	if _, err := o.wiki.CreateBlock(ctx, wikistore.CreateBlockInput{
		PageID:    page.ID,
		ProjectID: page.ProjectID,
		Position:  1.0,
		Type:      "text",
		Content:   map[string]any{"text": body.String()},
		ActorID:   task.OwnerID.String(),
	}); err != nil {
		return fmt.Errorf("create block: %w", err)
	}
	return o.store.Complete(ctx, task.ID, page.ID)
}

// Recover re-adopts in-flight tasks that died with the process. Called
// once on boot (see main.go). It scans for active tasks whose updated_at
// is older than the stuck cutoff — anything still marked
// searching/synthesizing/saving across a restart is, by construction,
// orphaned (the orchestrator that owned it is gone) — and re-runs each
// through the normal pipeline. run()'s resume logic + savePage's dup
// guard make re-runs safe: a task that had already saved its page just
// flips to done; one mid-synthesis resumes from its web results.
//
// Re-runs go through Run(), so they share the MaxConcurrent sem and
// can't flood model-relay on boot.
func (o *Orchestrator) Recover(ctx context.Context) error {
	const stuckCutoff = 5 * time.Minute
	stuck, err := o.store.ListStuck(ctx, stuckCutoff)
	if err != nil {
		return fmt.Errorf("list stuck research tasks: %w", err)
	}
	if len(stuck) == 0 {
		return nil
	}
	o.logger.Info("research: recovering stuck tasks", "count", len(stuck))
	for _, t := range stuck {
		t := t
		go o.Run(ctx, t.ID)
	}
	return nil
}
