// Package embed — backfill entries.embedding from model-relay
// /v1/embeddings. Mirrors the digest worker's queue/concurrency/SignFor
// pattern; the protocol is much simpler so the worker is roughly half
// the size.
//
// Why a separate worker (not folded into digest):
//   - digest needs a chat LLM (slow, high token cost, prompt template);
//     embed needs an embedding model (fast, cheap, single shot)
//   - failure modes differ: digest retries are about transient LLM
//     errors / parse failures; embed retries are pure HTTP transport
//   - operational toggle: digest can be off (no AI summary) while embed
//     is on (radar still works), and vice versa
//
// Backfill cadence: same 5min ticker as digest, runs after digest so
// new entries get takeaway+topics first then embedding for radar.
//
// Vector dim is fixed at 1024 (bge-m3). If admin swaps the embedding
// model to a different dimension, schema migration must precede.

package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	rssmetrics "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

const (
	defaultModel       = "bge-m3"
	defaultMaxInput    = 4000 // bge-m3 max ~ 8192 tokens; cap at chars
	defaultConcurrency = 2
	defaultQueueSize   = 256
	defaultTimeout     = 20 * time.Second
	maxRetries         = 3
)

// Job is one entry waiting to be embedded.
type Job struct {
	EntryID     uuid.UUID
	Title       string
	ContentText string
	OwnerUserID string // for SignFor; empty when scope=org/global
}

type Worker struct {
	Pool          *pgxpool.Pool
	ModelRelayURL string
	Logger        *slog.Logger
	Concurrency   int

	// SignFor mints a user-scoped bearer JWT (same pattern as digest).
	SignFor     func(userID string) (string, error)
	SystemToken string

	Model string

	HTTP    *http.Client
	Queue   chan Job
	wg      sync.WaitGroup
	stopped chan struct{}
}

func New(pool *pgxpool.Pool, modelRelayURL string) *Worker {
	return &Worker{
		Pool:          pool,
		ModelRelayURL: strings.TrimRight(modelRelayURL, "/"),
		Logger:        slog.Default(),
		Concurrency:   defaultConcurrency,
		HTTP:          &http.Client{Timeout: defaultTimeout},
		Queue:         make(chan Job, defaultQueueSize),
		stopped:       make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w.Concurrency <= 0 {
		w.Concurrency = defaultConcurrency
	}
	for i := 0; i < w.Concurrency; i++ {
		w.wg.Add(1)
		go w.run(ctx, i)
	}
	go func() {
		w.wg.Wait()
		close(w.stopped)
	}()
}

// Submit drops the job if the queue is full. Backfill picks it up next tick.
func (w *Worker) Submit(j Job) {
	select {
	case w.Queue <- j:
	default:
	}
}

func (w *Worker) run(ctx context.Context, _ int) {
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
	text := strings.TrimSpace(j.Title + "\n" + j.ContentText)
	if len(text) > defaultMaxInput {
		text = text[:defaultMaxInput]
	}
	if text == "" {
		// 空内容写空 marker (model='skip') 防止反复入队
		_ = w.markSkip(ctx, j.EntryID)
		return
	}

	token := w.resolveToken(j.OwnerUserID)
	model := w.Model
	if model == "" {
		model = defaultModel
	}

	start := time.Now()
	vec, err := w.callWithRetry(ctx, model, text, token)
	dur := time.Since(start).Seconds()
	if err != nil {
		w.Logger.Warn("embed: call failed", "entry", j.EntryID, "err", err.Error())
		rssmetrics.RecordDigestCall("error", dur) // reuse digest histogram for now;
		// dedicated embed metric in M8.5 polish.
		// 失败不持久化 ai_error (那是 digest 的字段); 下一轮 backfill 会重试.
		return
	}
	if len(vec) == 0 {
		rssmetrics.RecordDigestCall("empty", dur)
		return
	}
	if err := w.write(ctx, j.EntryID, vec, model); err != nil {
		w.Logger.Error("embed: write", "err", err.Error(), "entry", j.EntryID)
		return
	}
	rssmetrics.RecordDigestCall("ok", dur)
}

func (w *Worker) resolveToken(ownerUserID string) string {
	if ownerUserID != "" && w.SignFor != nil {
		if t, err := w.SignFor(ownerUserID); err == nil {
			return t
		}
	}
	return w.SystemToken
}

var errPermFail = errors.New("permanent failure")

func (w *Worker) callWithRetry(ctx context.Context, model, text, token string) ([]float32, error) {
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"input": text,
	})
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		v, err := w.callOnce(ctx, body, token)
		if err == nil {
			return v, nil
		}
		lastErr = err
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
	return nil, fmt.Errorf("embed: %d retries exhausted: %w", maxRetries, lastErr)
}

func (w *Worker) callOnce(ctx context.Context, body []byte, token string) ([]float32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		w.ModelRelayURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("%w: auth %d %s", errPermFail, resp.StatusCode,
			truncate(string(respBytes), 100))
	}
	if resp.StatusCode == 400 {
		return nil, fmt.Errorf("%w: bad request %s", errPermFail,
			truncate(string(respBytes), 100))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(respBytes), 100))
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", errPermFail, err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("%w: empty embedding", errPermFail)
	}
	return out.Data[0].Embedding, nil
}

func (w *Worker) write(ctx context.Context, id uuid.UUID, vec []float32, model string) error {
	v := pgvector.NewVector(vec)
	_, err := w.Pool.Exec(ctx, `
		UPDATE rss.entries
		   SET embedding=$2,
		       embedding_model=$3
		 WHERE id=$1`, id, v, model)
	return err
}

// markSkip — internal: 空内容 entry 写一个 sentinel 防止反复入队.
// 用 embedding_model='skip' 标记 (NULL embedding + non-NULL model).
func (w *Worker) markSkip(ctx context.Context, id uuid.UUID) error {
	_, err := w.Pool.Exec(ctx, `
		UPDATE rss.entries
		   SET embedding_model='skip'
		 WHERE id=$1 AND embedding IS NULL AND embedding_model IS NULL`, id)
	return err
}

// BackfillUnprocessed pulls entries lacking embedding and enqueues them.
// Mirrors digest.BackfillUnprocessed.
func (w *Worker) BackfillUnprocessed(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if w.ModelRelayURL == "" {
		return 0, nil
	}
	rows, err := w.Pool.Query(ctx, `
		SELECT e.id, e.title,
		       COALESCE(NULLIF(e.content_text, ''), e.content_html) AS body,
		       f.scope, f.scope_id
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE e.embedding IS NULL
		   AND e.embedding_model IS NULL  -- skip already-marked-skip rows
		   AND (length(coalesce(e.content_text,''))+length(coalesce(e.title,''))) > 20
		 ORDER BY e.fetched_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("embed: backfill query: %w", err)
	}
	defer rows.Close()
	enqueued := 0
	for rows.Next() {
		var j Job
		var scope, scopeID string
		if err := rows.Scan(&j.EntryID, &j.Title, &j.ContentText, &scope, &scopeID); err != nil {
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

// EmbedQuery — synchronous one-off embedding for rule SemanticQuery.
// Doesn't go through the queue (rule create/update is rare) and uses
// the system token so it works during user-context calls without
// reaching back into the JWT signer.
//
// Returns (vector, modelCode, err). modelCode comes from cfg / default,
// caller persists it to watch_rules.semantic_embedding_model.
func (w *Worker) EmbedQuery(ctx context.Context, text string) ([]float32, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, "", errors.New("embed: empty query")
	}
	if len(text) > defaultMaxInput {
		text = text[:defaultMaxInput]
	}
	model := w.Model
	if model == "" {
		model = defaultModel
	}
	// Rule embed prefers system token (dev) — caller-context propagation
	// would need the action handler to thread bearer through, M9 polish.
	vec, err := w.callWithRetry(ctx, model, text, w.SystemToken)
	if err != nil {
		return nil, "", err
	}
	return vec, model, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
