// Synthesizer — orchestrates: today picks → script → /v1/audio/speech →
// audio_cache. Public surface is one method (SynthForUser) that returns
// either a cached row or a freshly synthesised mp3.
//
// Why pull TodayPicks via the rss.TodayPicker interface (not direct DB):
// keeps briefing decoupled from today's internal Picks struct shape.
// The SDKAdapter already projects to rss.TodayPicks so we just consume
// that.
//
// 关键决策:
//   - 24h cache: 同一 (user_id, today_date) 用同一段 mp3, 即使重复请求
//   - bytes 直接存 PG bytea — 100KB / 用户 / 天, 1k 用户 100MB/天, 7d
//     滚动 < 1GB, PG 完全 OK; R2 / S3 等到 DAU 上千再考虑
//   - 失败不缓存空响应 — 下次重试; 缓存一份坏数据更糟
//
// 计费:
//   - 真合成时 model-relay 已经按字符在 hold/settle, briefing 不重复计
//   - 命中缓存零成本 — outcome="cached" metric 区分
//
// 失败模式:
//   - model-relay 网络: 502/timeout → 返 ErrUpstream, 不缓存
//   - voice 不合法 (我们写死了 longanyang for v3-plus, 不会发生; 但 admin
//     换 model 时可能踩到) → 502 from model-relay, 同上
//   - audio_cache 写失败 — 仍返 mp3 给客户端 (合成已成功, cache 是优化)

package briefing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	rssmetrics "github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultModel   = "cosyvoice-v3-plus"
	defaultVoice   = "longanyang"
	defaultFormat  = "mp3"
	defaultTTL     = 24 * time.Hour
	defaultTimeout = 60 * time.Second
	maxAudioBytes  = 4 * 1024 * 1024 // 4 MB safety cap (~ 4 min mp3)
)

var ErrUpstream = errors.New("briefing: upstream tts failed")

type Result struct {
	Mp3         []byte
	Script      string
	Voice       string
	Model       string
	Characters  int
	Cached      bool
	GeneratedAt time.Time
	HeadlineN   int
}

type Synthesizer struct {
	Pool          *pgxpool.Pool
	ModelRelayURL string
	HTTP          *http.Client
	Logger        *slog.Logger

	Model  string
	Voice  string
	Format string

	// SignFor — same per-user JWT pattern as digest/embed workers.
	SignFor func(userID string) (string, error)
}

func New(pool *pgxpool.Pool, modelRelayURL string) *Synthesizer {
	return &Synthesizer{
		Pool:          pool,
		ModelRelayURL: strings.TrimRight(modelRelayURL, "/"),
		HTTP:          &http.Client{Timeout: defaultTimeout},
		Logger:        slog.Default(),
		Model:         defaultModel,
		Voice:         defaultVoice,
		Format:        defaultFormat,
	}
}

// PicksLoader — Synthesizer doesn't import services/app_center/internal/rss/today
// directly (would create a cycle when Synthesizer is wired into the
// SDK app from main.go). Caller passes a closure that returns the
// already-projected TodayPicks for the user.
type PicksLoader func(ctx context.Context, userID string) (*todayPicksLite, error)

// todayPicksLite — local mirror of rss.TodayPicks fields we care about,
// avoids forcing PicksLoader callers to import the SDK package.
type todayPicksLite struct {
	HeadlineIDs []string
	GeneratedAt time.Time
}

// SynthForUser — main entry point. Loads or computes the briefing audio.
// Caller (action handler) is responsible for projecting picks → Script
// before calling, because briefing.FromPicks needs the full TodayPicks
// (with title/takeaway/feed_title) which the action handler already has.
func (s *Synthesizer) SynthForUser(
	ctx context.Context, userID string, scriptText string, headlineIDs []string,
	headlineN int,
) (*Result, error) {
	if s.Pool == nil {
		return nil, errors.New("briefing: nil pool")
	}
	if s.ModelRelayURL == "" {
		return nil, errors.New("briefing: model-relay url not set")
	}
	if scriptText == "" {
		return nil, errors.New("briefing: empty script")
	}

	hash := contentHash(headlineIDs)
	today := time.Now().UTC().Format("2006-01-02")

	// 1. Cache lookup — same user, same date, same content_hash, not expired.
	if cached, ok := s.lookup(ctx, userID, today, hash); ok {
		rssmetrics.RecordBriefing("cached", 0)
		return cached, nil
	}

	// 2. Fresh synth.
	start := time.Now()
	mp3, characters, err := s.callTTS(ctx, userID, scriptText)
	dur := time.Since(start).Seconds()
	if err != nil {
		rssmetrics.RecordBriefing("error", dur)
		return nil, err
	}
	rssmetrics.RecordBriefing("ok", dur)

	res := &Result{
		Mp3:         mp3,
		Script:      scriptText,
		Voice:       s.Voice,
		Model:       s.Model,
		Characters:  characters,
		Cached:      false,
		GeneratedAt: time.Now().UTC(),
		HeadlineN:   headlineN,
	}

	// 3. Persist cache. Failure is non-fatal — we already have the mp3.
	if err := s.store(ctx, userID, today, hash, res); err != nil {
		s.Logger.Warn("briefing: cache store failed", "user", userID, "err", err.Error())
	}
	return res, nil
}

// contentHash — deterministic signature of the picks set; caller-provided
// IDs in original order. Same picks → same hash → cache hit.
func contentHash(headlineIDs []string) []byte {
	h := sha256.New()
	for _, id := range headlineIDs {
		h.Write([]byte(id))
		h.Write([]byte("\x00"))
	}
	out := h.Sum(nil)
	return out
}

func (s *Synthesizer) lookup(ctx context.Context, userID, date string, hash []byte) (*Result, bool) {
	row := s.Pool.QueryRow(ctx, `
		SELECT script, mp3, voice, model, characters, created_at
		  FROM rss.audio_cache
		 WHERE user_id=$1 AND generated_date=$2 AND content_hash=$3
		   AND expires_at > now()`,
		userID, date, hash)
	var (
		script string
		mp3    []byte
		voice  string
		model  string
		chars  int
		genAt  time.Time
	)
	err := row.Scan(&script, &mp3, &voice, &model, &chars, &genAt)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && err.Error() != "no rows in result set" {
			s.Logger.Warn("briefing: cache lookup", "err", err.Error())
		}
		return nil, false
	}
	return &Result{
		Mp3: mp3, Script: script, Voice: voice, Model: model,
		Characters: chars, Cached: true, GeneratedAt: genAt,
	}, true
}

func (s *Synthesizer) store(ctx context.Context, userID, date string, hash []byte, r *Result) error {
	expires := r.GeneratedAt.Add(defaultTTL)
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO rss.audio_cache
			(user_id, generated_date, content_hash, script, mp3, voice, model,
			 characters, duration_ms, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0,$9,$10)
		ON CONFLICT (user_id, generated_date) DO UPDATE
		   SET content_hash = EXCLUDED.content_hash,
		       script       = EXCLUDED.script,
		       mp3          = EXCLUDED.mp3,
		       voice        = EXCLUDED.voice,
		       model        = EXCLUDED.model,
		       characters   = EXCLUDED.characters,
		       created_at   = EXCLUDED.created_at,
		       expires_at   = EXCLUDED.expires_at`,
		userID, date, hash, r.Script, r.Mp3, r.Voice, r.Model,
		r.Characters, r.GeneratedAt, expires)
	return err
}

func (s *Synthesizer) callTTS(ctx context.Context, userID, text string) ([]byte, int, error) {
	body, _ := json.Marshal(map[string]any{
		"model":           s.Model,
		"voice":           s.Voice,
		"input":           text,
		"response_format": s.Format,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.ModelRelayURL+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("briefing: build req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	token := s.resolveToken(userID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, 0, fmt.Errorf("%w: status %d: %s",
			ErrUpstream, resp.StatusCode, truncate(string(errBody), 200))
	}

	mp3, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: read body: %v", ErrUpstream, err)
	}
	if len(mp3) == 0 {
		// 不可能命中 (model-relay fix 后空流转 502), 但兜底.
		return nil, 0, fmt.Errorf("%w: empty audio", ErrUpstream)
	}

	// Characters: model-relay 返 audio bytes 不带 usage header; 用 input
	// rune count 估 (跟 model-relay 自己的 fallback 一致).
	chars := 0
	for range text {
		chars++
	}
	return mp3, chars, nil
}

func (s *Synthesizer) resolveToken(userID string) string {
	if s.SignFor != nil && userID != "" {
		if t, err := s.SignFor(userID); err == nil {
			return t
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
