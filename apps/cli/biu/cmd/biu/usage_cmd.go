// `biu usage` — summarise persisted usage records from
// ~/.biu/usage.jsonl.
//
// Examples:
//
//   biu usage                  # last 7 days, by day, all models
//   biu usage --since 30d      # last 30 days
//   biu usage --bucket month   # group by month
//   biu usage --model claude-opus-4-7
//   biu usage --since 7d --json   # machine-readable

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
	"github.com/spf13/cobra"
)

func newUsageCmd(_ *rootFlags) *cobra.Command {
	var (
		since    string
		bucket   string
		modelStr string
		jsonOut  bool
	)
	c := &cobra.Command{
		Use:   "usage",
		Short: "Summarise persisted token usage from ~/.biu/usage.jsonl",
		RunE: func(cmd *cobra.Command, _ []string) error {
			records, err := cost.LoadAll("")
			if err != nil {
				return err
			}
			if modelStr != "" {
				filtered := records[:0]
				for _, r := range records {
					if r.Model == modelStr {
						filtered = append(filtered, r)
					}
				}
				records = filtered
			}

			cutoff, err := parseSince(since)
			if err != nil {
				return err
			}
			b, err := parseBucket(bucket)
			if err != nil {
				return err
			}
			rows := cost.Aggregate(records, b, cutoff)
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			fmt.Println(cost.FormatTable(rows))

			// Total line for quick read.
			var total cost.Summary
			for _, r := range rows {
				total.Input += r.Input
				total.Output += r.Output
				total.CacheRead += r.CacheRead
				total.CacheWrite += r.CacheWrite
				total.USD += r.USD
				total.Turns += r.Turns
			}
			if total.Turns > 0 {
				fmt.Printf("\ntotal:        %-25s  %6d  %8d  %8d  %8d  $%9.4f\n",
					"(all)", total.Turns, total.Input, total.Output,
					total.CacheRead, total.USD)
			}
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "7d",
		`time window: 7d / 30d / 90d / all (or RFC3339 timestamp)`)
	c.Flags().StringVar(&bucket, "bucket", "day",
		`grouping: day | week | month`)
	c.Flags().StringVar(&modelStr, "model", "", "filter by exact model id")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a table")
	return c
}

// parseSince accepts "7d" / "30d" / "all" / RFC3339. Empty defaults
// to 7 days. Returns the zero time when no cutoff applies.
func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "all" {
		return time.Time{}, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return time.Time{}, fmt.Errorf("bad --since %q (e.g. 7d, 30d)", s)
		}
		return time.Now().Add(-time.Duration(n) * 24 * time.Hour), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since must be Nd or RFC3339; got %q", s)
	}
	return t, nil
}

func parseBucket(s string) (cost.Bucket, error) {
	switch strings.ToLower(s) {
	case "", "day":
		return cost.BucketDay, nil
	case "week":
		return cost.BucketWeek, nil
	case "month":
		return cost.BucketMonth, nil
	}
	return "", fmt.Errorf("--bucket must be day|week|month; got %q", s)
}
