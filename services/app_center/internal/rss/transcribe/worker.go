// Package transcribe turns podcast audio enclosures into searchable,
// AI-digestible text by calling model-relay's /v1/audio/transcriptions
// (OpenAI-compatible ASR, JSON async path → audio_url).
//
// It mirrors the digest / embed worker shape: a bounded worker pool fed by
// Submit() + a periodic BackfillUnprocessed scan, with a per-user JWT minted
// via SignFor so model-relay bills the feed owner's credits (the primary
// cost control). A secondary in-memory per-user daily audio-minutes cap
// guards against a runaway podcast feed before the credit system even sees
// it.
//
// Only scope='user' feeds are transcribed — org feeds have an org id (not a
// user id) as their scope_id, so there's no single user to bill; they're
// skipped for now.
package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// paraformer-v2 is dashscope's async ASR (cheapest + strongest Chinese);
	// the JSON path in model-relay routes to its AsyncTranscribeAdaptor.
	// Overridable via RSS_TRANSCRIBE_MODEL.
	defaultModel = "paraformer-v2"
	// model-relay blocks (submit+poll) up to ~10min per episode; give the
	// HTTP client headroom beyond that.
	defaultTimeout     = 12 * time.Minute
	defaultConcurrency = 2 // audio is heavy + long; keep the pool small
	defaultQueueSize   = 128
	// Per-user daily audio cap (seconds). 30 min of audio/user/day.
	defaultDailyCapSec = 30 * 60
)

type Job struct {
	EntryID     uuid.UUID
	OwnerUserID string // feed.scope_id when scope='user'
	AudioURL    string
}

type Worker struct {
	Pool          *pgxpool.Pool
	ModelRelayURL string
	Logger        *slog.Logger
	Concurrency   int

	// SignFor mints a user-scoped bearer JWT so model-relay bills/quotas
	// the owning user (same pattern as digest/embed/briefing).
	SignFor func(userID string) (string, error)

	// Model — model-relay catalog code (audio_transcription mode). Defaults
	// to defaultModel; set from RSS_TRANSCRIBE_MODEL.
	Model string

	// DailyCapSec — per-user daily audio seconds cap. 0 → defaultDailyCapSec.
	DailyCapSec int

	HTTP    *http.Client
	Queue   chan Job
	wg      sync.WaitGroup
	stopped chan struct{}

	// usage tracks per-user transcribed seconds per UTC day (in-memory;
	// resets on restart — acceptable as a defensive guard, the durable cost
	// control is model-relay credits).
	mu    sync.Mutex
	usage map[string]int // key: userID|YYYY-MM-DD → seconds
}

func New(pool *pgxpool.Pool, modelRelayURL string) *Worker {
	return &Worker{
		Pool:          pool,
		ModelRelayURL: modelRelayURL,
		Logger:        slog.Default(),
		Concurrency:   defaultConcurrency,
		Model:         defaultModel,
		DailyCapSec:   defaultDailyCapSec,
		HTTP:          &http.Client{Timeout: defaultTimeout},
		Queue:         make(chan Job, defaultQueueSize),
		stopped:       make(chan struct{}),
		usage:         map[string]int{},
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w.ModelRelayURL == "" {
		w.Logger.Warn("transcribe: MODEL_RELAY_URL empty; podcast transcription disabled")
		close(w.stopped)
		return
	}
	conc := w.Concurrency
	if conc <= 0 {
		conc = defaultConcurrency
	}
	w.Logger.Info("transcribe: workers starting", "concurrency", conc, "model", w.model())
	for i := 0; i < conc; i++ {
		w.wg.Add(1)
		go w.run(ctx, i)
	}
	go func() {
		w.wg.Wait()
		close(w.stopped)
	}()
}

func (w *Worker) Submit(j Job) {
	if w.ModelRelayURL == "" || j.AudioURL == "" {
		return
	}
	select {
	case w.Queue <- j:
	default:
		w.Logger.Warn("transcribe: queue full, drop", "entry_id", j.EntryID)
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
	// Per-user daily cap — fail fast with a quota error so the partial
	// index excludes the row (we won't retry it today).
	if !w.allow(j.OwnerUserID, 0) {
		w.markErr(ctx, j.EntryID, "quota: 今日转写时长已达上限")
		return
	}

	text, durationSec, segJSON, err := w.callOnce(ctx, j)
	if err != nil {
		w.Logger.Warn("transcribe: call failed", "entry", j.EntryID, "err", err.Error())
		w.markErr(ctx, j.EntryID, err.Error())
		return
	}
	if text == "" {
		w.markErr(ctx, j.EntryID, "transcription empty")
		return
	}
	w.record(j.OwnerUserID, durationSec)
	if err := w.write(ctx, j.EntryID, text, segJSON); err != nil {
		w.Logger.Error("transcribe: write", "err", err.Error(), "entry", j.EntryID)
	} else {
		w.Logger.Info("transcribe: done", "entry", j.EntryID,
			"chars", len(text), "duration_s", durationSec)
	}
}

type transcribeReq struct {
	Model    string `json:"model"`
	AudioURL string `json:"audio_url"`
}

// callOnce returns (transcript text, duration seconds, segments JSON). The
// segments JSON is the raw [{id,start,end,text}] array for synced playback,
// or nil if the provider returned none.
func (w *Worker) callOnce(ctx context.Context, j Job) (string, int, []byte, error) {
	token := ""
	if w.SignFor != nil && j.OwnerUserID != "" {
		if t, err := w.SignFor(j.OwnerUserID); err == nil {
			token = t
		}
	}
	if token == "" {
		return "", 0, nil, fmt.Errorf("transcribe: no bearer token for user %q", j.OwnerUserID)
	}

	body, _ := json.Marshal(transcribeReq{Model: w.model(), AudioURL: j.AudioURL})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		w.ModelRelayURL+"/v1/audio/transcriptions", bytes.NewReader(body))
	if err != nil {
		return "", 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", 0, nil, err
	}
	defer resp.Body.Close()
	var raw map[string]any
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&raw); err != nil {
		return "", 0, nil, fmt.Errorf("transcribe: decode (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 400 {
		return "", 0, nil, fmt.Errorf("transcribe: upstream %d: %s",
			resp.StatusCode, extractErrMsg(raw))
	}
	text, _ := raw["text"].(string)
	durationSec := 0
	if d, ok := raw["duration"].(float64); ok {
		durationSec = int(d)
	}
	var segJSON []byte
	if segs, ok := raw["segments"].([]any); ok && len(segs) > 0 {
		segJSON, _ = json.Marshal(segs)
	}
	return text, durationSec, segJSON, nil
}

// write replaces the (empty/short) episode body with the transcript and
// clears ai_processed_at so the digest worker re-summarizes the now-filled
// content. word_count / reading_seconds are recomputed from the transcript.
func (w *Worker) write(ctx context.Context, id uuid.UUID, text string, segJSON []byte) error {
	wc := len([]rune(text)) / 2 // rough zh/en blend; reading_seconds = wc*60/200
	readingSec := wc * 60 / 200
	var segs any // nil → SQL NULL
	if len(segJSON) > 0 {
		segs = segJSON
	}
	_, err := w.Pool.Exec(ctx, `
		UPDATE rss.entries
		   SET content_text        = $2,
		       transcribed_at      = now(),
		       word_count          = $3,
		       reading_seconds     = $4,
		       transcript_segments = $5,
		       ai_processed_at     = NULL,
		       ai_error            = ''
		 WHERE id = $1`,
		id, text, wc, readingSec, segs)
	return err
}

// markErr stamps transcribed_at so the partial index excludes the row (we
// don't endlessly retry a podcast model-relay can't transcribe), recording
// the reason in ai_error for surfacing in the reader.
func (w *Worker) markErr(ctx context.Context, id uuid.UUID, msg string) {
	_, _ = w.Pool.Exec(ctx, `
		UPDATE rss.entries
		   SET transcribed_at = now(),
		       ai_error       = $2
		 WHERE id = $1`,
		id, truncate(msg, 500))
}

// BackfillUnprocessed scans audio entries not yet transcribed and enqueues
// them. Run on a low-frequency ticker + once at boot.
func (w *Worker) BackfillUnprocessed(ctx context.Context, limit int) (int, error) {
	if w.ModelRelayURL == "" {
		return 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := w.Pool.Query(ctx, `
		SELECT e.id, e.enclosure_url, f.scope, f.scope_id
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE e.enclosure_url IS NOT NULL
		   AND e.transcribed_at IS NULL
		   AND f.scope = 'user'
		 ORDER BY e.fetched_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("transcribe: backfill query: %w", err)
	}
	defer rows.Close()
	enqueued := 0
	for rows.Next() {
		var j Job
		var scope, scopeID string
		if err := rows.Scan(&j.EntryID, &j.AudioURL, &scope, &scopeID); err != nil {
			continue
		}
		j.OwnerUserID = scopeID
		w.Submit(j)
		enqueued++
	}
	return enqueued, rows.Err()
}

// ─── per-user daily cap ───────────────────────────────────────────────

func (w *Worker) capSec() int {
	if w.DailyCapSec > 0 {
		return w.DailyCapSec
	}
	return defaultDailyCapSec
}

// allow reports whether userID may transcribe `add` more seconds today.
// add=0 just checks the current ceiling (used before a call whose duration
// is unknown until it returns).
func (w *Worker) allow(userID string, add int) bool {
	if userID == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.usage[w.key(userID)]+add < w.capSec()
}

func (w *Worker) record(userID string, sec int) {
	if userID == "" || sec <= 0 {
		return
	}
	w.mu.Lock()
	w.usage[w.key(userID)] += sec
	w.mu.Unlock()
}

func (w *Worker) key(userID string) string {
	return userID + "|" + nowUTCDate()
}

func (w *Worker) model() string {
	if w.Model != "" {
		return w.Model
	}
	return defaultModel
}

func nowUTCDate() string {
	return time.Now().UTC().Format("2006-01-02")
}

// extractErrMsg pulls a human-readable reason out of an error response.
// model-relay uses {"error":{"code","message"}} (nested object); some
// upstreams use flat {"error":"..."} or {"message":"..."}. Without this,
// the nested form left ai_error as a bare "upstream 502:" with no reason.
func extractErrMsg(raw map[string]any) string {
	switch e := raw["error"].(type) {
	case map[string]any:
		if m, ok := e["message"].(string); ok && m != "" {
			return m
		}
		if c, ok := e["code"].(string); ok {
			return c
		}
	case string:
		if e != "" {
			return e
		}
	}
	if m, ok := raw["message"].(string); ok {
		return m
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
