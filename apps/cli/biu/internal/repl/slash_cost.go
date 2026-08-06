// /cost slash — the long-form cumulative report. Two variants:
//
//   /cost            — session aggregate: tokens (split by kind) +
//                       cache hit ratio + USD + last-turn context %
//   /cost --by-tool  — F4 per-tool leaderboard: Calls / ElapsedMs /
//                       OutputBytes / Errors, sorted by elapsed desc
//
// Status bar's short rendering lives in statusbar.go::costAndContextNote;
// this is the inspection variant.

package repl

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
)

func (m model) costNote() string {
	dur := time.Since(m.startedAt).Round(time.Second)
	if m.engine == nil {
		return fmt.Sprintf("turns=%d  uptime=%s  (token usage TBD: legacy chat path; configure direct/cloud mode for cost tracking)",
			m.turnsCount, dur)
	}
	snap := m.engine.Cost().Snapshot()

	var b strings.Builder
	fmt.Fprintf(&b, "session: model=%s · turns=%d · uptime=%s\n",
		snap.Model, m.turnsCount, dur)

	totalInput := snap.InputTokens + snap.CacheReadTokens + snap.CacheWriteTokens
	fmt.Fprintf(&b, "\ntokens (cumulative across all turns):\n")
	fmt.Fprintf(&b, "  input         %s\n", formatThousands(snap.InputTokens))
	fmt.Fprintf(&b, "  cache read    %s\n", formatThousands(snap.CacheReadTokens))
	fmt.Fprintf(&b, "  cache write   %s\n", formatThousands(snap.CacheWriteTokens))
	fmt.Fprintf(&b, "  output        %s\n", formatThousands(snap.OutputTokens))
	fmt.Fprintf(&b, "  ─────────────────────\n")
	fmt.Fprintf(&b, "  total input   %s  (cache hit %s)\n",
		formatThousands(totalInput), formatPercent(snap.CacheHitRate()))

	fmt.Fprintf(&b, "\ncost: $%.4f USD\n", snap.USD)

	// Last-turn context-window usage. P20.6 status-bar feeds the same
	// numbers; repeat here so users running /cost manually don't have
	// to glance at the bar.
	if m.lastUsageInput > 0 || m.lastUsageCacheRead > 0 || m.lastUsageCacheCreate > 0 {
		usage := cost.ContextUsage{
			InputTokens:       m.lastUsageInput,
			CacheReadTokens:   m.lastUsageCacheRead,
			CacheCreateTokens: m.lastUsageCacheCreate,
		}
		pct := cost.ContextPercent(usage, m.modelID)
		window := cost.ContextWindowForModel(m.modelID)
		fmt.Fprintf(&b, "\ncontext (last turn): %s / %s  (%d%% used)  %s\n",
			formatThousands(usage.Total()),
			formatThousands(window),
			pct.Used,
			contextBar(pct.Used))
	}

	return strings.TrimRight(b.String(), "\n")
}

// costByToolNote 把 Tracker.SnapshotByTool() 渲染成 leaderboard。
// LLM token 不摊到工具(input 是上下文,output 是模型自身),只列工具自然
// 能归属的指标:Calls / ElapsedMs / OutputBytes / Errors。按 ElapsedMs 倒
// 排,慢工具最显眼;同时算 totals 让用户一眼看见整体。
//
// 没数据时给一个友好提示,不输出空表(避免误以为命令失败)。
func (m model) costByToolNote() string {
	if m.engine == nil {
		return "/cost --by-tool: requires engine path (set [providers.anthropic].api_key + mode=direct)."
	}
	byTool := m.engine.Cost().SnapshotByTool()
	if len(byTool) == 0 {
		return "/cost --by-tool: no tool calls recorded yet — run a turn that uses tools first."
	}

	type row struct {
		name  string
		usage cost.ToolUsage
	}
	rows := make([]row, 0, len(byTool))
	for name, u := range byTool {
		rows = append(rows, row{name: name, usage: u})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].usage.ElapsedMs != rows[j].usage.ElapsedMs {
			return rows[i].usage.ElapsedMs > rows[j].usage.ElapsedMs
		}
		// 时间相等(常见场景:都很快)按 calls 倒排,再按名字字典序保证稳定。
		if rows[i].usage.Calls != rows[j].usage.Calls {
			return rows[i].usage.Calls > rows[j].usage.Calls
		}
		return rows[i].name < rows[j].name
	})

	// 列宽计算 —— Tool 列至少 20 (容下 "EnterPlanMode" 这类长名)。
	const minNameW = 20
	nameW := minNameW
	for _, r := range rows {
		if len(r.name) > nameW {
			nameW = len(r.name)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "tool usage (this session):\n\n")
	fmt.Fprintf(&b, "  %-*s  %6s  %12s  %12s  %6s\n",
		nameW, "Tool", "Calls", "Elapsed", "Output", "Errors")
	fmt.Fprintf(&b, "  %s  %s  %s  %s  %s\n",
		strings.Repeat("─", nameW), strings.Repeat("─", 6),
		strings.Repeat("─", 12), strings.Repeat("─", 12),
		strings.Repeat("─", 6))

	var totalCalls, totalErrs int
	var totalElapsed, totalBytes int64
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-*s  %6d  %12s  %12s  %6d\n",
			nameW, r.name,
			r.usage.Calls,
			formatDurationMs(r.usage.ElapsedMs),
			formatBytes(r.usage.OutputBytes),
			r.usage.Errors,
		)
		totalCalls += r.usage.Calls
		totalErrs += r.usage.Errors
		totalElapsed += r.usage.ElapsedMs
		totalBytes += r.usage.OutputBytes
	}
	fmt.Fprintf(&b, "  %s  %s  %s  %s  %s\n",
		strings.Repeat("─", nameW), strings.Repeat("─", 6),
		strings.Repeat("─", 12), strings.Repeat("─", 12),
		strings.Repeat("─", 6))
	fmt.Fprintf(&b, "  %-*s  %6d  %12s  %12s  %6d\n",
		nameW, "Total",
		totalCalls,
		formatDurationMs(totalElapsed),
		formatBytes(totalBytes),
		totalErrs,
	)

	fmt.Fprintf(&b, "\nnote: LLM tokens are tracked at session level (`/cost`); per-tool\n")
	fmt.Fprintf(&b, "      figures here cover what the runner can attribute cleanly\n")
	fmt.Fprintf(&b, "      (call count, wall time, result bytes, errors). Token cost\n")
	fmt.Fprintf(&b, "      is upstream of any single tool — see Cost() for that.\n")
	return strings.TrimRight(b.String(), "\n")
}

// formatDurationMs 把 ms 转成人类可读字符串(<1s 用 "350ms",>=1s 用
// "1.4s",>=60s 用 "1m12s")。`/cost --by-tool` 的工具时长跨度可能很大,
// 统一一种单位反而不直观。
func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60_000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	min := ms / 60_000
	sec := (ms % 60_000) / 1000
	return fmt.Sprintf("%dm%ds", min, sec)
}
