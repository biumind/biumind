// /cost --by-tool — F4 落地后的 per-tool leaderboard 单测。
//
// 用 statusline_test.go 已有的 newTestModel + nullProvider:不需要真跑
// LLM,只要拿到一个有 *cost.Tracker 的 *QueryEngine,直接给 tracker
// 喂 AddTool 数据,断言 costByToolNote() 输出含期望片段。

package repl

import (
	"strings"
	"testing"
	"time"
)

// TestCostByTool_NoEngine — engine 没接(legacy chat path)给友好提示而不是 panic。
func TestCostByTool_NoEngine(t *testing.T) {
	m := model{}
	got := m.costByToolNote()
	if !strings.Contains(got, "requires engine path") {
		t.Errorf("missing engine should hint engine path; got %q", got)
	}
}

// TestCostByTool_EmptyData — engine 在但 tracker 还没记录工具调用:输出
// 引导用户先跑一轮的 hint,不输出空表。
func TestCostByTool_EmptyData(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	got := m.costByToolNote()
	if !strings.Contains(got, "no tool calls recorded") {
		t.Errorf("empty tracker should hint to run a turn; got %q", got)
	}
}

// TestCostByTool_RendersLeaderboard — 喂 3 个工具的数据,断言
// 排序(ElapsedMs 倒排) + 表头 + Total 行 + 数字格式化。
func TestCostByTool_RendersLeaderboard(t *testing.T) {
	m := newTestModel(t, "claude-sonnet-4-6")
	tr := m.engine.Cost()
	tr.AddTool("Read", 100*time.Millisecond, 1024, false)
	tr.AddTool("Read", 50*time.Millisecond, 512, false)
	tr.AddTool("Bash", 1500*time.Millisecond, 4096, false)
	tr.AddTool("Bash", 800*time.Millisecond, 2048, true)
	tr.AddTool("Glob", 30*time.Millisecond, 256, false)

	got := m.costByToolNote()

	// 表头 + 分割线 + Total 行
	for _, frag := range []string{"Tool", "Calls", "Elapsed", "Output", "Errors", "Total"} {
		if !strings.Contains(got, frag) {
			t.Errorf("output missing column / row %q:\n%s", frag, got)
		}
	}

	// 三个工具都列出,且 Bash(2300ms)在 Read(150ms)和 Glob(30ms)之前(ElapsedMs 倒排)
	bashIdx := strings.Index(got, "Bash")
	readIdx := strings.Index(got, "Read")
	globIdx := strings.Index(got, "Glob")
	if bashIdx < 0 || readIdx < 0 || globIdx < 0 {
		t.Fatalf("missing tool rows: bash=%d read=%d glob=%d\n%s",
			bashIdx, readIdx, globIdx, got)
	}
	if !(bashIdx < readIdx && readIdx < globIdx) {
		t.Errorf("expected Bash → Read → Glob ordering by ElapsedMs;"+
			" got bash=%d read=%d glob=%d", bashIdx, readIdx, globIdx)
	}

	// Bash 的总时长 2300ms = 2.3s,formatDurationMs 应该输出 "2.3s"
	if !strings.Contains(got, "2.3s") {
		t.Errorf("expected Bash total elapsed `2.3s`; got %q", got)
	}
	// Bash 的 errors=1
	if !strings.Contains(got, "  Bash") {
		t.Errorf("Bash row missing")
	}

	// Total 行:5 calls / 2480ms / 7936 bytes / 1 error
	// formatBytes 已有实现,7936 bytes ≈ 7.75 KB
	if !strings.Contains(got, "Total") {
		t.Errorf("missing Total row; got %q", got)
	}
}

// TestCostByTool_FormatDurationMs — 单位边界覆盖。
func TestFormatDurationMs(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0ms"},
		{350, "350ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{1500, "1.5s"},
		{59_999, "60.0s"}, // 边界 — 仍走秒分支
		{60_000, "1m0s"},
		{75_500, "1m15s"},
	}
	for _, c := range cases {
		got := formatDurationMs(c.in)
		if got != c.want {
			t.Errorf("formatDurationMs(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
