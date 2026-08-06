package compact

import (
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ─── config loading ─────────────────────────────────────────

func TestDefaultTimeBasedMC(t *testing.T) {
	cfg := DefaultTimeBasedMC()
	if cfg.Enabled {
		t.Error("default should be disabled")
	}
	if cfg.GapThresholdMinutes != 60 {
		t.Errorf("default gap = %d, want 60", cfg.GapThresholdMinutes)
	}
	if cfg.KeepRecent != 5 {
		t.Errorf("default keep = %d, want 5", cfg.KeepRecent)
	}
}

func TestLoadTimeBasedMCFromEnv(t *testing.T) {
	t.Setenv("BIU_TIME_BASED_MC", "1")
	t.Setenv("BIU_TIME_BASED_MC_GAP_MIN", "30")
	t.Setenv("BIU_TIME_BASED_MC_KEEP_RECENT", "3")

	cfg := LoadTimeBasedMCFromEnv()
	if !cfg.Enabled {
		t.Error("should enable")
	}
	if cfg.GapThresholdMinutes != 30 {
		t.Errorf("gap = %d, want 30", cfg.GapThresholdMinutes)
	}
	if cfg.KeepRecent != 3 {
		t.Errorf("keep = %d, want 3", cfg.KeepRecent)
	}
}

func TestLoadTimeBasedMCFromEnv_invalidIgnored(t *testing.T) {
	t.Setenv("BIU_TIME_BASED_MC_GAP_MIN", "garbage")
	t.Setenv("BIU_TIME_BASED_MC_KEEP_RECENT", "0")
	cfg := LoadTimeBasedMCFromEnv()
	if cfg.GapThresholdMinutes != 60 || cfg.KeepRecent != 5 {
		t.Errorf("invalid env should fall back to defaults, got %+v", cfg)
	}
}

// ─── trigger evaluation ─────────────────────────────────────

func TestEvaluateTimeBasedTrigger_disabled(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: false, GapThresholdMinutes: 60, KeepRecent: 5}
	msgs := []state.Message{
		{Role: state.RoleAssistant, CreatedAt: time.Now().Add(-2 * time.Hour)},
	}
	if got := EvaluateTimeBasedTrigger(msgs, cfg, time.Now()); got != nil {
		t.Errorf("disabled config should never trigger: %+v", got)
	}
}

func TestEvaluateTimeBasedTrigger_belowThreshold(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 5}
	msgs := []state.Message{
		{Role: state.RoleAssistant, CreatedAt: time.Now().Add(-30 * time.Minute)},
	}
	if got := EvaluateTimeBasedTrigger(msgs, cfg, time.Now()); got != nil {
		t.Errorf("30min gap < 60min threshold should not trigger: %+v", got)
	}
}

func TestEvaluateTimeBasedTrigger_fires(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 5}
	now := time.Now()
	msgs := []state.Message{
		{Role: state.RoleAssistant, CreatedAt: now.Add(-90 * time.Minute)},
	}
	got := EvaluateTimeBasedTrigger(msgs, cfg, now)
	if got == nil {
		t.Fatal("90min gap should trigger")
	}
	if got.GapMinutes < 89 || got.GapMinutes > 91 {
		t.Errorf("gap = %f, want ~90", got.GapMinutes)
	}
}

func TestEvaluateTimeBasedTrigger_noAssistant(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 5}
	msgs := []state.Message{
		{Role: state.RoleUser, CreatedAt: time.Now().Add(-2 * time.Hour)},
	}
	if got := EvaluateTimeBasedTrigger(msgs, cfg, time.Now()); got != nil {
		t.Error("no assistant message → no trigger")
	}
}

// ─── ApplyTimeBasedMC ───────────────────────────────────────

// helper: build a message with N tool_result blocks.
func userWithToolResults(ids ...string) state.Message {
	blocks := make([]state.ContentBlock, len(ids))
	for i, id := range ids {
		blocks[i] = state.ContentBlock{
			Type:      state.ContentToolResult,
			ToolUseID: id,
			Text:      "result for " + id,
		}
	}
	return state.Message{Role: state.RoleUser, Content: blocks}
}

func TestApplyTimeBasedMC_clearsOldKeepsRecent(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 2}
	now := time.Now()
	msgs := []state.Message{
		{Role: state.RoleAssistant, ID: "a", CreatedAt: now.Add(-90 * time.Minute)},
		userWithToolResults("t1", "t2", "t3", "t4", "t5"),
	}
	trigger := EvaluateTimeBasedTrigger(msgs, cfg, now)
	if trigger == nil {
		t.Fatal("should trigger")
	}
	cleared := ApplyTimeBasedMC(msgs, trigger)
	if cleared != 3 { // t1, t2, t3 cleared; t4 + t5 kept
		t.Errorf("cleared = %d, want 3", cleared)
	}
	for _, b := range msgs[1].Content {
		switch b.ToolUseID {
		case "t1", "t2", "t3":
			if b.Text != TimeBasedMCClearedMessage {
				t.Errorf("%s should be cleared, got %q", b.ToolUseID, b.Text)
			}
		case "t4", "t5":
			if b.Text == TimeBasedMCClearedMessage {
				t.Errorf("%s should be kept, got cleared", b.ToolUseID)
			}
		}
	}
}

func TestApplyTimeBasedMC_keepFloorAt1(t *testing.T) {
	// KeepRecent=0 must NOT clear everything — clearing every result
	// leaves the model with no working context.
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 0}
	now := time.Now()
	msgs := []state.Message{
		{Role: state.RoleAssistant, ID: "a", CreatedAt: now.Add(-90 * time.Minute)},
		userWithToolResults("t1", "t2"),
	}
	trigger := EvaluateTimeBasedTrigger(msgs, cfg, now)
	cleared := ApplyTimeBasedMC(msgs, trigger)
	if cleared != 1 {
		t.Errorf("KeepRecent=0 should floor to 1; cleared = %d, want 1", cleared)
	}
	if msgs[1].Content[1].Text == TimeBasedMCClearedMessage {
		t.Error("last result should always be kept")
	}
}

func TestApplyTimeBasedMC_idempotent(t *testing.T) {
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 1}
	now := time.Now()
	msgs := []state.Message{
		{Role: state.RoleAssistant, ID: "a", CreatedAt: now.Add(-90 * time.Minute)},
		userWithToolResults("t1", "t2", "t3"),
	}
	trigger := EvaluateTimeBasedTrigger(msgs, cfg, now)
	first := ApplyTimeBasedMC(msgs, trigger)
	second := ApplyTimeBasedMC(msgs, trigger)
	if first == 0 {
		t.Error("first pass should clear something")
	}
	if second != 0 {
		t.Errorf("second pass should be idempotent (0 cleared), got %d", second)
	}
}

func TestApplyTimeBasedMC_nilTriggerNoop(t *testing.T) {
	msgs := []state.Message{userWithToolResults("t1", "t2")}
	cleared := ApplyTimeBasedMC(msgs, nil)
	if cleared != 0 {
		t.Errorf("nil trigger should be a no-op, cleared = %d", cleared)
	}
	for _, b := range msgs[0].Content {
		if b.Text == TimeBasedMCClearedMessage {
			t.Error("nil trigger should not modify messages")
		}
	}
}

func TestApplyTimeBasedMC_keepBeyondCount(t *testing.T) {
	// KeepRecent > total tool results → nothing to clear.
	cfg := TimeBasedMCConfig{Enabled: true, GapThresholdMinutes: 60, KeepRecent: 10}
	now := time.Now()
	msgs := []state.Message{
		{Role: state.RoleAssistant, ID: "a", CreatedAt: now.Add(-90 * time.Minute)},
		userWithToolResults("t1", "t2"),
	}
	trigger := EvaluateTimeBasedTrigger(msgs, cfg, now)
	if got := ApplyTimeBasedMC(msgs, trigger); got != 0 {
		t.Errorf("keep > count → 0 cleared, got %d", got)
	}
}
