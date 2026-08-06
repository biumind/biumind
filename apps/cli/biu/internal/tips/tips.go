// Package tips surfaces short, contextual hints to the user without
// being annoying. Each tip has a stable ID, a predicate that says
// "is now the right moment", and a body. The scheduler picks at most
// one tip per session-start and never repeats a tip the user has
// already seen N times (default: 3).
//
// Kept deliberately small: the value is the framework itself plus a
// starter set of tips that point users at biu features they probably
// haven't discovered yet.
//
// The history file (~/.biumind/tips-shown.json) carries per-tip
// counts so the suppression survives restarts. When a tip is no
// longer relevant (Predicate() returns false), the scheduler skips
// it without incrementing — fixing your config shouldn't burn the
// budget for that tip's appearances.

package tips

import (
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Tip is one registered hint. Keep IDs stable — they're the
// suppression key.
type Tip struct {
	// ID is the cross-version stable identifier. Lowercase
	// hyphenated, e.g. "doctor-on-startup-error".
	ID string

	// Title is shown bold-prefixed in the output. Optional —
	// when empty the body alone is rendered.
	Title string

	// Body is the user-facing one to three lines. Markdown OK;
	// the REPL renderer handles formatting.
	Body string

	// Weight biases random selection when multiple tips qualify.
	// Higher = more likely. Default 1.
	Weight int

	// Predicate decides whether the tip applies right now. nil
	// means "always applicable". Should be cheap (no I/O on the
	// hot path) — the scheduler runs every registered predicate
	// per call.
	Predicate func() bool
}

// MaxImpressions is how many times a single tip may surface for
// the same user before suppression kicks in — a hard cap so the
// framework never turns into a nag.
const MaxImpressions = 3

// Registry holds every available tip.
type Registry struct {
	mu   sync.RWMutex
	tips []Tip
}

// NewRegistry returns an empty registry. Built-in tips need to be
// registered explicitly so test fixtures stay deterministic.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a tip. Empty IDs / nil predicates / zero-body tips
// are silently skipped (defensive against mistakes in the bundled
// tip set).
func (r *Registry) Register(t Tip) {
	if t.ID == "" || t.Body == "" {
		return
	}
	if t.Weight <= 0 {
		t.Weight = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tips = append(r.tips, t)
}

// All returns a snapshot of registered tips in registration order.
func (r *Registry) All() []Tip {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tip, len(r.tips))
	copy(out, r.tips)
	return out
}

// History tracks how many times each tip has been shown. Persisted
// to ~/.biumind/tips-shown.json so suppression survives restart.
type History struct {
	Counts map[string]int       `json:"counts"`
	LastAt map[string]time.Time `json:"last_at"`
}

// LoadHistory reads the on-disk history. Missing file → empty
// history (not an error).
func LoadHistory() (*History, error) {
	path, err := historyPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &History{
			Counts: map[string]int{},
			LastAt: map[string]time.Time{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var h History
	if err := json.Unmarshal(data, &h); err != nil {
		// Corrupt file: start fresh rather than fail the REPL.
		return &History{
			Counts: map[string]int{},
			LastAt: map[string]time.Time{},
		}, nil
	}
	if h.Counts == nil {
		h.Counts = map[string]int{}
	}
	if h.LastAt == nil {
		h.LastAt = map[string]time.Time{}
	}
	return &h, nil
}

// Save persists the history atomically (temp + rename).
func (h *History) Save() error {
	if h == nil {
		return nil
	}
	path, err := historyPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// MarkShown records that tipID has been displayed. Idempotent at
// the call level — the count increments on every call (so multiple
// MarkShown for the same tip in one session count multiple times).
// Caller decides whether to MarkShown after every render or once
// per session.
func (h *History) MarkShown(tipID string) {
	if h == nil || tipID == "" {
		return
	}
	h.Counts[tipID]++
	h.LastAt[tipID] = time.Now().UTC()
}

// Choose picks a tip from the registry to surface. Returns nil
// when no tip qualifies (predicates all false, all over-shown, or
// registry empty). Selection rules:
//
//  1. Filter to tips whose Predicate passes (or is nil).
//  2. Filter out tips whose history.Counts[id] >= MaxImpressions.
//  3. Weighted random pick across the survivors.
//
// rng is exposed for deterministic tests; production callers pass
// nil to use the default time-seeded source.
func Choose(reg *Registry, history *History, rng *rand.Rand) *Tip {
	if reg == nil {
		return nil
	}
	candidates := []Tip{}
	for _, t := range reg.All() {
		if t.Predicate != nil && !t.Predicate() {
			continue
		}
		if history != nil && history.Counts[t.ID] >= MaxImpressions {
			continue
		}
		candidates = append(candidates, t)
	}
	if len(candidates) == 0 {
		return nil
	}
	// Stable order under same input — sort by ID before random
	// pick so tests are deterministic when the same RNG is used.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ID < candidates[j].ID
	})

	totalWeight := 0
	for _, c := range candidates {
		totalWeight += c.Weight
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	pick := rng.Intn(totalWeight)
	for _, c := range candidates {
		if pick < c.Weight {
			out := c
			return &out
		}
		pick -= c.Weight
	}
	// Unreachable but defensive.
	out := candidates[0]
	return &out
}

// Render formats a Tip for display. Title (when set) goes on its
// own line above the body; both are trimmed. Caller handles any
// terminal styling via the standard system-note channel.
func (t Tip) Render() string {
	if t.Title == "" {
		return t.Body
	}
	return "💡 " + t.Title + "\n" + t.Body
}

// historyPath returns the on-disk location for the history file.
func historyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biumind", "tips-shown.json"), nil
}
