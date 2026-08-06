// Package digest — AI summary worker for rss.entries.
//
// One worker pool per app_center process. Entries arrive via the
// in-memory channel `Queue` (the rss scheduler enqueues each newly-
// inserted entry). Workers pull, call model-relay /v1/messages, parse
// JSON, write back to rss.entries. SHA256(content_text) is the cache
// key — same content from different feeds reuses the LLM call.
//
// Failure handling:
//   - upstream HTTP/timeout    → exponential backoff retry (3 attempts)
//   - JSON parse / shape       → mark ai_error, no retry (deterministic)
//   - model-relay 401/403      → mark ai_error, no retry
//   - all retries exhausted    → mark ai_error with last err string
//
// `ai_error != ''` is the dead-letter signal for the unprocessed
// index, so the worker doesn't reattempt on every refresh.

package digest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Default model — overridable via Worker.Model after construction
	// (main.go reads RSS_DIGEST_MODEL env). The dev model-relay catalog
	// only exposes glm-5.1 active by default; set RSS_DIGEST_MODEL to
	// switch in prod once Anthropic Haiku is provisioned.
	defaultModel       = "glm-5.1"
	defaultMaxTokens   = 400
	defaultMaxContent  = 4000 // input chars cap (≈ 4k tokens for zh)
	defaultTimeout     = 25 * time.Second
	defaultConcurrency = 8
	defaultQueueSize   = 256
	maxRetries         = 3
)

type Job struct {
	EntryID     uuid.UUID
	Title       string
	Source      string // human-readable source name
	ContentText string
	UserToken   string // bearer for model-relay; empty = use SystemToken
	OwnerUserID string // feed.scope_id when scope='user'; used by main.go signer
}

type Worker struct {
	Pool          *pgxpool.Pool
	ModelRelayURL string
	Logger        *slog.Logger
	Concurrency   int

	// SystemToken is a fallback bearer used when neither Job.UserToken
	// nor SignFor produces a token. Mostly useful for tests.
	SystemToken string

	// SignFor mints a user-scoped bearer JWT for the given user_id.
	// Wired by main.go using the shared JWT_SECRET so the call to
	// model-relay is billed / quota'd against the real owning user
	// of the feed (not a synthetic service account that lacks BYOK
	// credentials in the model-relay catalog).
	SignFor func(userID string) (string, error)

	// Model — model-relay catalog code. Defaults to defaultModel when
	// empty. Set this from main.go via RSS_DIGEST_MODEL env so ops
	// can swap models without a rebuild.
	Model string

	HTTP    *http.Client
	Queue   chan Job
	cache   *sync.Map // sha256 → digestResult (in-process; restart resets)
	wg      sync.WaitGroup
	stopped chan struct{}
}

type digestResult struct {
	Takeaway   string   `json:"takeaway"`
	Bullets    []string `json:"bullets"`
	Importance int      `json:"importance"`
	Lang       string   `json:"lang"`
	Topics     []string `json:"topics"`
}

func New(pool *pgxpool.Pool, modelRelayURL string) *Worker {
	return &Worker{
		Pool:          pool,
		ModelRelayURL: modelRelayURL,
		Logger:        slog.Default(),
		Concurrency:   defaultConcurrency,
		HTTP:          &http.Client{Timeout: defaultTimeout},
		Queue:         make(chan Job, defaultQueueSize),
		cache:         &sync.Map{},
		stopped:       make(chan struct{}),
	}
}

// Start launches the worker pool. Call once at boot. Returns
// immediately; workers keep running until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	if w.ModelRelayURL == "" {
		w.Logger.Warn("digest: MODEL_RELAY_URL empty; AI digest disabled")
		close(w.stopped)
		return
	}
	conc := w.Concurrency
	if conc <= 0 {
		conc = defaultConcurrency
	}
	w.Logger.Info("digest: workers starting", "concurrency", conc, "model", defaultModel)
	for i := 0; i < conc; i++ {
		w.wg.Add(1)
		go w.run(ctx, i)
	}
	go func() {
		w.wg.Wait()
		close(w.stopped)
	}()
}

// Submit enqueues a job. Non-blocking; drops on full queue with a
// warn log (the periodic backfill scanner will pick it up next tick).
func (w *Worker) Submit(j Job) {
	if w.ModelRelayURL == "" {
		return // AI disabled, no-op
	}
	select {
	case w.Queue <- j:
	default:
		w.Logger.Warn("digest: queue full, drop", "entry_id", j.EntryID)
	}
}

func (w *Worker) run(ctx context.Context, idx int) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-w.Queue:
			if !ok {
				return
			}
			w.process(ctx, j)
		}
	}
}

func (w *Worker) process(ctx context.Context, j Job) {
	key := contentHash(j.ContentText)
	if cached, ok := w.cache.Load(key); ok {
		if err := w.write(ctx, j.EntryID, cached.(*digestResult), ""); err != nil {
			w.Logger.Error("digest: write cached", "err", err.Error(), "entry", j.EntryID)
		}
		return
	}

	start := time.Now()
	res, err := w.callOnce(ctx, j)
	dur := time.Since(start).Seconds()
	if err != nil {
		w.Logger.Warn("digest: call failed", "entry", j.EntryID, "err", err.Error())
		rss.RecordDigestCall("error", dur)
		_ = w.write(ctx, j.EntryID, nil, err.Error())
		return
	}
	if res == nil || (res.Takeaway == "" && len(res.Bullets) == 0 && len(res.Topics) == 0) {
		rss.RecordDigestCall("empty", dur)
	} else {
		rss.RecordDigestCall("ok", dur)
	}
	w.cache.Store(key, res)
	if err := w.write(ctx, j.EntryID, res, ""); err != nil {
		w.Logger.Error("digest: write", "err", err.Error(), "entry", j.EntryID)
	}
}

func (w *Worker) callOnce(ctx context.Context, j Job) (*digestResult, error) {
	content := j.ContentText
	if len(content) > defaultMaxContent {
		content = content[:defaultMaxContent]
	}

	// Resolve token: prefer the caller's own bearer (pass-through for
	// in-context invokes); fall back to a freshly-minted per-user token
	// (preserves quota/billing isolation); finally the global SystemToken.
	token := j.UserToken
	if token == "" && w.SignFor != nil && j.OwnerUserID != "" {
		if t, err := w.SignFor(j.OwnerUserID); err == nil {
			token = t
		}
	}
	if token == "" {
		token = w.SystemToken
	}
	user := strings.NewReplacer(
		"{{TITLE}}", j.Title,
		"{{SOURCE}}", j.Source,
		"{{CONTENT}}", content,
	).Replace(UserPromptTemplate)

	model := w.Model
	if model == "" {
		model = defaultModel
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": defaultMaxTokens,
		"system":     SystemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": user},
		},
	}
	bodyJSON, _ := json.Marshal(body)

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		res, err := w.callMessages(ctx, bodyJSON, token)
		if err == nil {
			return res, nil
		}
		lastErr = err
		// Permanent failures don't retry.
		if errors.Is(err, errPermFail) {
			return nil, err
		}
		if attempt < maxRetries-1 {
			backoff := time.Duration(1<<attempt) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return nil, fmt.Errorf("digest: %d retries exhausted: %w", maxRetries, lastErr)
}

var errPermFail = errors.New("permanent failure")

func (w *Worker) callMessages(ctx context.Context, body []byte, token string) (*digestResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		w.ModelRelayURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("%w: auth %d %s", errPermFail, resp.StatusCode,
			truncate(string(respBytes), 100))
	}
	if resp.StatusCode == 400 {
		// Bad request usually means our prompt+content overshot a cap;
		// don't retry, retry won't fix the input.
		return nil, fmt.Errorf("%w: bad request %s", errPermFail,
			truncate(string(respBytes), 100))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(respBytes), 100))
	}

	// model-relay /v1/messages mostly returns Anthropic shape
	// (content[].text), but for some upstream models it relays the
	// OpenAI chat-completions shape (choices[].message.content) instead.
	// Accept both rather than silently extracting nothing.
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, fmt.Errorf("%w: decode upstream: %v", errPermFail, err)
	}
	var raw string
	for _, c := range out.Content {
		if c.Type == "text" {
			raw += c.Text
		}
	}
	if raw == "" {
		for _, ch := range out.Choices {
			raw += ch.Message.Content
		}
	}
	res, perr := parseDigest(raw)
	if perr != nil {
		// Surface the upstream body so a model/shape mismatch is diagnosable
		// instead of an opaque "no json object in \"\"".
		return nil, fmt.Errorf("%w [upstream: %s]", perr, truncate(string(respBytes), 500))
	}
	return res, nil
}

// parseDigest extracts and validates the digest JSON from raw model
// output. Tolerant to ``` fences, prefix/suffix prose, and missing
// optional fields.
func parseDigest(raw string) (*digestResult, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i > 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i > 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("%w: no json object in %q", errPermFail, truncate(raw, 100))
	}
	s = s[start : end+1]

	var r digestResult
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, fmt.Errorf("%w: %v", errPermFail, err)
	}
	r.Takeaway = strings.TrimSpace(r.Takeaway)
	if r.Takeaway == "" {
		return nil, fmt.Errorf("%w: empty takeaway", errPermFail)
	}
	r.Bullets = cleanList(r.Bullets, 3)
	r.Topics = cleanList(r.Topics, 5)
	if r.Importance < 1 || r.Importance > 3 {
		r.Importance = 1
	}
	r.Lang = strings.ToLower(strings.TrimSpace(r.Lang))
	if r.Lang != "zh" && r.Lang != "en" {
		r.Lang = "zh"
	}
	return &r, nil
}

func cleanList(in []string, max int) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= max {
			break
		}
	}
	return out
}

func (w *Worker) write(ctx context.Context, id uuid.UUID, r *digestResult, errMsg string) error {
	if r != nil {
		bulletsJSON, _ := json.Marshal(r.Bullets)
		_, err := w.Pool.Exec(ctx, `
			UPDATE rss.entries
			   SET ai_takeaway     = $2,
			       ai_bullets      = $3,
			       ai_topics       = $4,
			       ai_importance   = $5,
			       ai_lang         = $6,
			       ai_processed_at = now(),
			       ai_error        = ''
			 WHERE id = $1`,
			id, r.Takeaway, bulletsJSON, r.Topics, r.Importance, r.Lang)
		return err
	}
	// Failure path — mark error so the partial index excludes this row.
	_, err := w.Pool.Exec(ctx, `
		UPDATE rss.entries
		   SET ai_processed_at = now(),
		       ai_error        = $2
		 WHERE id = $1`,
		id, truncate(errMsg, 500))
	return err
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// BackfillUnprocessed scans entries with no AI processing yet and
// re-enqueues them. Run once at boot to recover from a crash mid-batch
// + on a low-frequency cron (e.g. every 5 min) as belt-and-braces in
// case Submit drops on full queue.
func (w *Worker) BackfillUnprocessed(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if w.ModelRelayURL == "" {
		return 0, nil
	}
	rows, err := w.Pool.Query(ctx, `
		SELECT e.id, e.title, COALESCE(f.title, '') AS source,
		       COALESCE(NULLIF(e.content_text, ''), e.content_html) AS body,
		       f.scope, f.scope_id
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE e.ai_processed_at IS NULL
		   AND e.ai_error = ''
		   AND (e.content_text != '' OR e.content_html != '')
		 ORDER BY e.fetched_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("digest: backfill query: %w", err)
	}
	defer rows.Close()
	enqueued := 0
	for rows.Next() {
		var j Job
		var scope, scopeID string
		if err := rows.Scan(&j.EntryID, &j.Title, &j.Source, &j.ContentText, &scope, &scopeID); err != nil {
			continue
		}
		if scope == "user" {
			j.OwnerUserID = scopeID
		}
		w.Submit(j)
		enqueued++
	}
	return enqueued, rows.Err()
}
