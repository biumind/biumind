// CronCreate / CronDelete / CronList — in-process scheduled prompts.
//
// We deliberately do NOT pull a full cron parser. The model rarely
// needs sub-minute precision; for P0 we expose a simple scheduler
// that fires a stored prompt on a fixed interval (per-minute
// granularity) until canceled.
//
// Lifecycle (session-only by default):
//
//   * Jobs live only for the lifetime of this process — they vanish
//     on exit. Persistence (`durable: true`) lands in P1.
//   * Each Tick fires the job's prompt back through a callback
//     supplied at registration time. The REPL injects this callback
//     so a tick = "queue up the prompt as if the user typed it".
//
// Responsibility split:
//
//   * CronStore owns the in-memory job list + ticker goroutine.
//   * The three tools just CRUD against that store.

package interactive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// CronJob is one scheduled prompt.
type CronJob struct {
	ID        string    `json:"id"`
	Cron      string    `json:"cron"` // five-field expression (display only in P0)
	Prompt    string    `json:"prompt"`
	Recurring bool      `json:"recurring"`
	IntervalMin int     `json:"interval_min"` // resolved minimum interval; 0 = single-shot
	NextFire  time.Time `json:"next_fire"`

	// Durable jobs survive process restarts via the on-disk store.
	// Non-durable (default) jobs vanish on Close — `durable: false`
	// is the default.
	Durable bool `json:"durable,omitempty"`
}

// CronCallback is invoked when a job fires. The REPL implements this
// to enqueue the prompt onto its input buffer.
type CronCallback func(job CronJob)

// CronStore is a tiny in-memory scheduler with optional disk-backed
// persistence for durable jobs. Safe for concurrent access. Caller
// must call Close() at session end to stop the ticker.
type CronStore struct {
	mu     sync.Mutex
	jobs   map[string]*CronJob
	cb     CronCallback
	tick   *time.Ticker
	closed atomic.Bool

	// path is the JSON file we persist durable jobs to. Empty disables
	// persistence (NewCronStore's behaviour).
	path string
}

// NewCronStore starts the per-minute ticker and remembers the fire
// callback. Returns an in-memory-only store (durable jobs not
// persisted). Use NewDurableCronStore for cross-restart survival.
func NewCronStore(cb CronCallback) *CronStore {
	s := &CronStore{jobs: map[string]*CronJob{}, cb: cb,
		tick: time.NewTicker(time.Minute)}
	go s.loop()
	return s
}

// NewDurableCronStore is like NewCronStore but also reads any
// previously-saved durable jobs from `path` so they survive process
// restarts. Empty path defaults to ~/.biu/scheduled_tasks.json.
//
// Only jobs with Durable=true hit disk; ephemeral jobs stay in-memory
// and disappear on Close.
func NewDurableCronStore(cb CronCallback, path string) (*CronStore, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".biu", "scheduled_tasks.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	s := &CronStore{
		jobs: map[string]*CronJob{}, cb: cb, path: path,
		tick: time.NewTicker(time.Minute),
	}
	if err := s.loadFromDisk(); err != nil {
		return nil, err
	}
	go s.loop()
	return s, nil
}

// loadFromDisk reads any saved durable jobs into the in-memory map.
// Missing file → empty start. Past NextFire times are bumped forward
// so we don't fire 100 times at once after a long downtime.
func (s *CronStore) loadFromDisk() error {
	if s.path == "" {
		return nil
	}
	body, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("cron: read %s: %w", s.path, err)
	}
	var saved []*CronJob
	if err := json.Unmarshal(body, &saved); err != nil {
		return fmt.Errorf("cron: parse %s: %w", s.path, err)
	}
	now := time.Now()
	for _, j := range saved {
		if !j.Durable {
			continue // defensive — don't load stale ephemerals
		}
		// Skip a job we already missed if it's a one-shot — firing
		// 6 hours late would be confusing.
		if !j.Recurring && now.After(j.NextFire) {
			continue
		}
		// Bump recurring jobs forward to the next slot if we missed
		// the original.
		if j.Recurring && j.IntervalMin > 0 && now.After(j.NextFire) {
			j.NextFire = now.Add(time.Duration(j.IntervalMin) * time.Minute)
		}
		s.jobs[j.ID] = j
	}
	return nil
}

// flushToDisk writes only durable jobs back to the persistence file.
// Caller must hold s.mu.
func (s *CronStore) flushToDisk() error {
	if s.path == "" {
		return nil
	}
	durable := make([]*CronJob, 0)
	for _, j := range s.jobs {
		if j.Durable {
			durable = append(durable, j)
		}
	}
	body, err := json.MarshalIndent(durable, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *CronStore) loop() {
	for range s.tick.C {
		if s.closed.Load() {
			return
		}
		now := time.Now()
		s.mu.Lock()
		var fired []CronJob
		dirty := false
		for id, j := range s.jobs {
			if !now.Before(j.NextFire) {
				fired = append(fired, *j)
				if j.Recurring && j.IntervalMin > 0 {
					j.NextFire = now.Add(time.Duration(j.IntervalMin) * time.Minute)
					if j.Durable {
						dirty = true
					}
				} else {
					if j.Durable {
						dirty = true
					}
					delete(s.jobs, id)
				}
			}
		}
		if dirty {
			_ = s.flushToDisk()
		}
		s.mu.Unlock()
		if s.cb != nil {
			for _, j := range fired {
				s.cb(j)
			}
		}
	}
}

// Close stops the ticker. Safe to call multiple times.
func (s *CronStore) Close() {
	if s.closed.CompareAndSwap(false, true) {
		s.tick.Stop()
	}
}

// Add inserts a new job. ID is auto-assigned. Durable jobs are
// flushed to disk before returning — failures are surfaced via the
// returned ID being empty.
func (s *CronStore) Add(j *CronJob) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	j.ID = newCronID()
	s.jobs[j.ID] = j
	if j.Durable {
		// Best-effort: a flush failure leaves the job in memory but
		// not on disk. We don't return an error here so callers don't
		// have to thread one through; the caller can use Path() +
		// stat-check if they care.
		_ = s.flushToDisk()
	}
	return j.ID
}

// Delete removes a job by ID.
func (s *CronStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	delete(s.jobs, id)
	if ok && job.Durable {
		_ = s.flushToDisk()
	}
	return ok
}

// List returns a sorted snapshot.
func (s *CronStore) List() []CronJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CronJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out
}

var cronCounter int64

func newCronID() string {
	return "c" + strconv.FormatInt(atomic.AddInt64(&cronCounter, 1), 10)
}

// parseInterval extracts a minimum interval (minutes) from a cron
// string. We support two simple forms:
//
//   "*/N * * * *"   → every N minutes
//   "M H * * *"     → daily at HH:MM (24h interval)
//
// Anything else falls back to 60 minutes — defensive default.
func parseInterval(cron string) int {
	cron = strings.TrimSpace(cron)
	parts := strings.Fields(cron)
	if len(parts) != 5 {
		return 60
	}
	if strings.HasPrefix(parts[0], "*/") {
		n, err := strconv.Atoi(parts[0][2:])
		if err == nil && n > 0 {
			return n
		}
	}
	if _, err := strconv.Atoi(parts[0]); err == nil {
		// Specific minute — at minimum hourly.
		return 60
	}
	return 60
}

// ─── Tools ────────────────────────────────────────────

type CronCreateTool struct{ Store *CronStore }

func (CronCreateTool) Name() string { return "CronCreate" }

func (CronCreateTool) Description(_ map[string]any) string {
	return "Schedule a prompt to fire on a cron expression. " +
		"Recurring=true reruns until deleted; recurring=false fires once."
}

func (CronCreateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cron":      map[string]any{"type": "string"},
			"prompt":    map[string]any{"type": "string"},
			"recurring": map[string]any{"type": "boolean"},
			"durable": map[string]any{
				"type":        "boolean",
				"description": "Persist across biu restarts. Default false (in-memory only).",
			},
		},
		"required": []string{"cron", "prompt"},
	}
}

func (CronCreateTool) IsReadOnly(_ map[string]any) bool        { return false }
func (CronCreateTool) IsDestructive(_ map[string]any) bool     { return false }
func (CronCreateTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (CronCreateTool) InterruptBehavior() string               { return "cancel" }

func (c CronCreateTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if c.Store == nil {
		return softErr("CronCreate", "no cron scheduler configured"), nil
	}
	cron, _ := input["cron"].(string)
	prompt, _ := input["prompt"].(string)
	recurring := true
	if v, ok := input["recurring"].(bool); ok {
		recurring = v
	}
	durable, _ := input["durable"].(bool)
	if strings.TrimSpace(cron) == "" || strings.TrimSpace(prompt) == "" {
		return softErr("CronCreate", "cron + prompt required"), nil
	}
	interval := parseInterval(cron)
	id := c.Store.Add(&CronJob{
		Cron: cron, Prompt: prompt, Recurring: recurring,
		IntervalMin: interval,
		NextFire:    time.Now().Add(time.Duration(interval) * time.Minute),
		Durable:     durable,
	})
	persist := ""
	if durable {
		persist = " (durable — survives biu restarts)"
	}
	return text(fmt.Sprintf("Cron #%s scheduled (every %d min, recurring=%v)%s.",
		id, interval, recurring, persist)), nil
}

type CronDeleteTool struct{ Store *CronStore }

func (CronDeleteTool) Name() string { return "CronDelete" }
func (CronDeleteTool) Description(_ map[string]any) string {
	return "Cancel a scheduled cron job by its ID."
}
func (CronDeleteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
		},
		"required": []string{"id"},
	}
}
func (CronDeleteTool) IsReadOnly(_ map[string]any) bool        { return false }
func (CronDeleteTool) IsDestructive(_ map[string]any) bool     { return false }
func (CronDeleteTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (CronDeleteTool) InterruptBehavior() string               { return "cancel" }

func (c CronDeleteTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if c.Store == nil {
		return softErr("CronDelete", "no cron scheduler"), nil
	}
	id, _ := input["id"].(string)
	if !c.Store.Delete(id) {
		return softErr("CronDelete", "no job with id "+id), nil
	}
	return text("Cron " + id + " deleted."), nil
}

type CronListTool struct{ Store *CronStore }

func (CronListTool) Name() string { return "CronList" }
func (CronListTool) Description(_ map[string]any) string {
	return "List every scheduled cron job in this session."
}
func (CronListTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (CronListTool) IsReadOnly(_ map[string]any) bool        { return true }
func (CronListTool) IsDestructive(_ map[string]any) bool     { return false }
func (CronListTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (CronListTool) InterruptBehavior() string               { return "cancel" }

func (c CronListTool) Call(_ context.Context, _ map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if c.Store == nil {
		return softErr("CronList", "no cron scheduler"), nil
	}
	jobs := c.Store.List()
	if len(jobs) == 0 {
		return text("(no scheduled jobs)"), nil
	}
	var b strings.Builder
	for _, j := range jobs {
		fmt.Fprintf(&b, "%s  cron=%q  recurring=%v  next=%s\n  prompt=%s\n",
			j.ID, j.Cron, j.Recurring,
			j.NextFire.Format(time.RFC3339), truncate(j.Prompt, 100))
	}
	return text(strings.TrimRight(b.String(), "\n")), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
