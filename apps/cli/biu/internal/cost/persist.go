// Cross-session usage persistence.
//
// Every completed turn appends one JSONL record to
// ~/.biu/usage.jsonl. The file is local-only — no telemetry leaves
// the box. Kept simple enough to grep / parse with awk.
//
// Wire shape (one line per turn):
//
//   {
//     "ts":           "2026-05-25T10:15:30Z",
//     "session_id":   "5ab6ad48...",
//     "model":        "claude-opus-4-7",
//     "input":        7,
//     "output":       138,
//     "cache_read":   12352,
//     "cache_write":  593,
//     "usd":          0.012345,
//     "elapsed_ms":   4595
//   }
//
// `biu usage` aggregates these by day / week / month + filters on
// model. We deliberately don't keep prompts / responses — privacy +
// disk hygiene.

package cost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// UsageRecord is one persisted turn.
type UsageRecord struct {
	TS         time.Time `json:"ts"`
	SessionID  string    `json:"session_id,omitempty"`
	Model      string    `json:"model"`
	Input      int       `json:"input"`
	Output     int       `json:"output"`
	CacheRead  int       `json:"cache_read"`
	CacheWrite int       `json:"cache_write"`
	USD        float64   `json:"usd"`
	ElapsedMS  int64     `json:"elapsed_ms,omitempty"`
}

// Logger appends UsageRecords to ~/.biu/usage.jsonl. Safe for
// concurrent use across goroutines.
type Logger struct {
	mu   sync.Mutex
	path string
}

// NewLogger returns a logger that writes to ~/.biu/usage.jsonl
// (parent dir auto-created). Override the path with override="" or
// a custom value.
func NewLogger(override string) (*Logger, error) {
	path := override
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".biu", "usage.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Logger{path: path}, nil
}

// Path returns the resolved file path (handy for `biu doctor`).
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Append writes one record. Errors are returned to the caller; biu
// engine logs to stderr and continues — usage logging must never
// fail a turn.
func (l *Logger) Append(r UsageRecord) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.TS.IsZero() {
		r.TS = time.Now().UTC()
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return err
	}
	_, err = f.Write([]byte{'\n'})
	return err
}

// ─── Read side: aggregation + reporting ───────────────

// LoadAll reads every record. Missing file returns (nil, nil) so
// `biu usage` works on a fresh install.
func LoadAll(path string) ([]UsageRecord, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".biu", "usage.jsonl")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []UsageRecord
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scan.Scan() {
		var r UsageRecord
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			continue // skip corrupt lines
		}
		out = append(out, r)
	}
	return out, scan.Err()
}

// Bucket key for aggregation grouping.
type Bucket string

const (
	BucketDay   Bucket = "day"
	BucketWeek  Bucket = "week"
	BucketMonth Bucket = "month"
)

// Summary is one aggregated row.
type Summary struct {
	Period     string // YYYY-MM-DD / YYYY-Www / YYYY-MM
	Model      string // empty when grouping ignores model
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
	USD        float64
	Turns      int
}

// Aggregate buckets records by (period, model). Sort: newest period
// first; within a period, model A→Z.
func Aggregate(records []UsageRecord, bucket Bucket, since time.Time) []Summary {
	type key struct {
		period, model string
	}
	agg := map[key]*Summary{}
	for _, r := range records {
		if !since.IsZero() && r.TS.Before(since) {
			continue
		}
		k := key{period: bucketKey(r.TS, bucket), model: r.Model}
		s, ok := agg[k]
		if !ok {
			s = &Summary{Period: k.period, Model: k.model}
			agg[k] = s
		}
		s.Input += r.Input
		s.Output += r.Output
		s.CacheRead += r.CacheRead
		s.CacheWrite += r.CacheWrite
		s.USD += r.USD
		s.Turns++
	}
	out := make([]Summary, 0, len(agg))
	for _, s := range agg {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Period != out[j].Period {
			return out[i].Period > out[j].Period // newest first
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func bucketKey(t time.Time, b Bucket) string {
	t = t.UTC()
	switch b {
	case BucketWeek:
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case BucketMonth:
		return t.Format("2006-01")
	}
	return t.Format("2006-01-02")
}

// FormatTable renders Summary slices as a fixed-width text table.
// Non-zero usd values format with 4 decimals.
func FormatTable(rows []Summary) string {
	if len(rows) == 0 {
		return "(no usage records)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-12s  %-25s  %6s  %8s  %8s  %8s  %10s\n",
		"period", "model", "turns", "input", "output", "cache_r", "usd")
	b.WriteString(strings.Repeat("─", 90))
	b.WriteByte('\n')
	for _, r := range rows {
		fmt.Fprintf(&b, "%-12s  %-25s  %6d  %8d  %8d  %8d  $%9.4f\n",
			r.Period, truncate(r.Model, 25), r.Turns,
			r.Input, r.Output, r.CacheRead, r.USD)
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
