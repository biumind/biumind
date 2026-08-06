// /usage slash — historical token + USD totals from
// ~/.biu/usage.jsonl.
//
// /cost shows the running session;
// /usage looks across the persisted ledger for cumulative spend
// over time. Default scope is "today"; flags widen to week / month /
// all-time, and a model filter narrows when only one matters.
//
// Read-only. Aggregation happens in-memory after a single jsonl
// scan, so even a year-long ledger is fast (most workdays log a
// few hundred entries).

package repl

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
)

// handleUsage parses sub-flags + renders the aggregated table.
//
// Forms:
//
//	/usage                — last 24h
//	/usage today          — same as bare
//	/usage week           — last 7 days
//	/usage month          — last 30 days
//	/usage all            — every record
//	/usage <model-prefix> — filter on model id (e.g. "opus")
func (m model) handleUsage(parts []string) string {
	scope := "today"
	modelFilter := ""
	for _, arg := range parts[1:] {
		switch strings.ToLower(arg) {
		case "today", "week", "month", "all":
			scope = strings.ToLower(arg)
		default:
			modelFilter = strings.ToLower(arg)
		}
	}

	records, err := cost.LoadAll("")
	if err != nil {
		return "/usage: read usage.jsonl: " + err.Error()
	}
	if len(records) == 0 {
		return "/usage: no records yet — usage logging activates on the first turn"
	}

	cutoff := scopeCutoff(scope)
	type bucket struct {
		Records int
		Input   int
		Output  int
		Cache   int
		USD     float64
	}
	byModel := map[string]*bucket{}
	totals := bucket{}
	for _, r := range records {
		if !cutoff.IsZero() && r.TS.Before(cutoff) {
			continue
		}
		if modelFilter != "" && !strings.Contains(strings.ToLower(r.Model), modelFilter) {
			continue
		}
		b, ok := byModel[r.Model]
		if !ok {
			b = &bucket{}
			byModel[r.Model] = b
		}
		b.Records++
		b.Input += r.Input
		b.Output += r.Output
		b.Cache += r.CacheRead + r.CacheWrite
		b.USD += r.USD
		totals.Records++
		totals.Input += r.Input
		totals.Output += r.Output
		totals.Cache += r.CacheRead + r.CacheWrite
		totals.USD += r.USD
	}

	if totals.Records == 0 {
		return fmt.Sprintf("/usage: no records in scope %q (filter=%q)", scope, modelFilter)
	}

	// Sort models by spend desc so the top spend model is at the
	// top of the table.
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool {
		return byModel[models[i]].USD > byModel[models[j]].USD
	})

	var b strings.Builder
	fmt.Fprintf(&b, "/usage: %s — %d turn(s)", scopeLabel(scope), totals.Records)
	if modelFilter != "" {
		fmt.Fprintf(&b, " (model filter: %q)", modelFilter)
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%-30s %8s %10s %10s %12s %10s\n",
		"model", "turns", "input", "output", "cache", "USD")
	fmt.Fprintln(&b, strings.Repeat("─", 84))
	for _, m := range models {
		v := byModel[m]
		fmt.Fprintf(&b, "%-30s %8d %10s %10s %12s %10.4f\n",
			truncate(m, 30), v.Records,
			humanInt(v.Input), humanInt(v.Output),
			humanInt(v.Cache), v.USD)
	}
	fmt.Fprintln(&b, strings.Repeat("─", 84))
	fmt.Fprintf(&b, "%-30s %8d %10s %10s %12s %10.4f\n",
		"total", totals.Records,
		humanInt(totals.Input), humanInt(totals.Output),
		humanInt(totals.Cache), totals.USD)

	return strings.TrimRight(b.String(), "\n")
}

// scopeCutoff returns the earliest timestamp that should be
// counted, or the zero time when no cutoff applies.
func scopeCutoff(scope string) time.Time {
	now := time.Now()
	switch scope {
	case "today":
		return now.Add(-24 * time.Hour)
	case "week":
		return now.Add(-7 * 24 * time.Hour)
	case "month":
		return now.Add(-30 * 24 * time.Hour)
	case "all":
		return time.Time{}
	}
	return now.Add(-24 * time.Hour)
}

func scopeLabel(scope string) string {
	switch scope {
	case "today":
		return "last 24h"
	case "week":
		return "last 7 days"
	case "month":
		return "last 30 days"
	case "all":
		return "all-time"
	}
	return "last 24h"
}

// humanInt renders an int as a thousands-separated string. Cheaper
// than pulling in a formatter library for one slash.
func humanInt(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	first := len(s) % 3
	if first > 0 {
		b.WriteString(s[:first])
	}
	for i := first; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
