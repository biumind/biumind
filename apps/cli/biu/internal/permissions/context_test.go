package permissions

import (
	"strings"
	"testing"
)

func TestParseRuleStringreference(t *testing.T) {
	cases := []struct {
		in   string
		want RuleValue
	}{
		{"Bash", RuleValue{ToolName: "Bash"}},
		{"Bash(npm install)", RuleValue{ToolName: "Bash", RuleContent: "npm install"}},
		{"Bash(npm:*)", RuleValue{ToolName: "Bash", RuleContent: "npm:*"}},
		{"Bash()", RuleValue{ToolName: "Bash"}},
		{"Bash(*)", RuleValue{ToolName: "Bash"}},
		{`Bash(echo "hi\(\)")`, RuleValue{ToolName: "Bash", RuleContent: `echo "hi()"`}},
		{"Edit(/tmp/**)", RuleValue{ToolName: "Edit", RuleContent: "/tmp/**"}},
		{"bash:ls", RuleValue{ToolName: "bash", RuleContent: "ls:*"}}, // legacy promoted
		{"read:**", RuleValue{ToolName: "read"}},
	}
	for _, c := range cases {
		got := ParseRuleString(c.in)
		if got != c.want {
			t.Errorf("%q → %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestMatchToolBashPrefix(t *testing.T) {
	rv := ParseRuleString("Bash(git:*)")
	if !rv.MatchTool("Bash", map[string]any{"command": "git status"}) {
		t.Errorf("git:* should match `git status`")
	}
	if !rv.MatchTool("Bash", map[string]any{"command": "git"}) {
		t.Errorf("git:* should match bare `git`")
	}
	if rv.MatchTool("Bash", map[string]any{"command": "gitlab"}) {
		t.Errorf("git:* must not match `gitlab`")
	}
}

func TestMatchToolBashWildcard(t *testing.T) {
	rv := ParseRuleString("Bash(curl https://*)")
	if !rv.MatchTool("Bash", map[string]any{"command": "curl https://example.com"}) {
		t.Errorf("wildcard match failed")
	}
	if rv.MatchTool("Bash", map[string]any{"command": "curl http://example.com"}) {
		t.Errorf("wildcard must not match http")
	}
}

func TestMatchToolEditGlob(t *testing.T) {
	rv := ParseRuleString("Edit(/repo/**)")
	if !rv.MatchTool("Edit", map[string]any{"path": "/repo/foo/bar.go"}) {
		t.Errorf("/repo/** should cover nested paths")
	}
	if rv.MatchTool("Edit", map[string]any{"path": "/other/foo.go"}) {
		t.Errorf("/repo/** must not cover sibling tree")
	}
}

func TestDecideDenyOverridesAllow(t *testing.T) {
	c := NewContext()
	c.AddRules(SrcUserSettings, BehaviorAllow, []string{"Bash(rm -rf /)"})
	c.AddRules(SrcLocalSettings, BehaviorDeny, []string{"Bash(rm:*)"})
	d, r := Decide(c, Request{Tool: "Bash", Args: map[string]any{"command": "rm -rf /tmp"}})
	if d != DecideDeny {
		t.Errorf("deny rule must beat allow; got %v (%+v)", d, r)
	}
	if r.Source != SrcLocalSettings {
		t.Errorf("decision source = %v, want localSettings", r.Source)
	}
}

func TestDecidePlanBlocksWrites(t *testing.T) {
	c := NewContext()
	c.SetMode(ModePlan)
	d, _ := Decide(c, Request{Tool: "Edit", Args: map[string]any{"path": "/x.go"}})
	if d != DecideDeny {
		t.Errorf("plan must deny edits, got %v", d)
	}
	d, _ = Decide(c, Request{Tool: "Read", IsReadOnly: true, Args: map[string]any{"path": "/x.go"}})
	if d != DecideAllow {
		t.Errorf("plan must allow reads, got %v", d)
	}
}

func TestDecideAcceptEditsAllowsEdit(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeAcceptEdits)
	d, _ := Decide(c, Request{Tool: "Edit", Args: map[string]any{"path": "/x.go"}})
	if d != DecideAllow {
		t.Errorf("acceptEdits must allow edit, got %v", d)
	}
	d, _ = Decide(c, Request{Tool: "Bash", IsDestructive: true, Args: map[string]any{"command": "rm -rf x"}})
	if d != DecideAsk {
		t.Errorf("acceptEdits must still ask on destructive bash, got %v", d)
	}
}

func TestDecideBypass(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeBypass)
	d, _ := Decide(c, Request{Tool: "Bash", IsDestructive: true,
		Args: map[string]any{"command": "rm -rf /"}})
	if d != DecideAllow {
		t.Errorf("bypass must allow everything, got %v", d)
	}
}

func TestDecideSessionGrant(t *testing.T) {
	c := NewContext()
	args := map[string]any{"command": "make build"}
	c.Grant(SessionGrantKey("Bash", args))
	d, r := Decide(c, Request{Tool: "Bash", Args: args})
	if d != DecideAllow || r.Kind != "session" {
		t.Errorf("session grant must auto-allow; got %v reason=%+v", d, r)
	}
}

func TestDecideAllowedPromptSemantic(t *testing.T) {
	c := NewContext()
	c.AddAllowedPrompts([]AllowedPrompt{
		{Tool: "Bash", Prompt: "run tests"},
	})
	// Concrete command the model issues — different surface form,
	// same intent. Classifier must pick this up.
	d, r := Decide(c, Request{
		Tool: "Bash",
		Args: map[string]any{"command": "go test ./..."},
	})
	if d != DecideAllow {
		t.Fatalf("allowedPrompt match should allow; got %v reason=%+v", d, r)
	}
	if r.Kind != "allowedPrompt" {
		t.Errorf("Reason.Kind should be allowedPrompt; got %q", r.Kind)
	}
}

func TestDecideAllowedPromptDoesNotMatchUnrelated(t *testing.T) {
	c := NewContext()
	c.AddAllowedPrompts([]AllowedPrompt{
		{Tool: "Bash", Prompt: "run tests"},
	})
	d, _ := Decide(c, Request{
		Tool: "Bash",
		Args: map[string]any{"command": "rm -rf /important"},
	})
	if d == DecideAllow {
		t.Errorf("rm -rf must not match `run tests`; got allow")
	}
}

func TestDecideAllowedPromptVetoedByDeny(t *testing.T) {
	c := NewContext()
	c.AddAllowedPrompts([]AllowedPrompt{
		{Tool: "Bash", Prompt: "run tests"},
	})
	c.AddRules(SrcUserSettings, BehaviorDeny, []string{"Bash(go test:*)"})
	d, r := Decide(c, Request{
		Tool: "Bash",
		Args: map[string]any{"command": "go test ./..."},
	})
	if d != DecideDeny {
		t.Fatalf("explicit deny must beat allowedPrompt; got %v", d)
	}
	if r.Kind != "rule" {
		t.Errorf("expected rule reason after deny veto; got %q", r.Kind)
	}
}

func TestAllowedPromptsSnapshotAndClear(t *testing.T) {
	c := NewContext()
	c.AddAllowedPrompts([]AllowedPrompt{
		{Tool: "Bash", Prompt: "run tests"},
		{Tool: "Bash", Prompt: "build"},
	})
	// Idempotent: re-adding same pair is a no-op.
	c.AddAllowedPrompts([]AllowedPrompt{
		{Tool: "Bash", Prompt: "build"},
	})
	if got := c.AllowedPrompts(); len(got) != 2 {
		t.Errorf("expected 2 entries (dedupe); got %v", got)
	}
	c.ClearAllowedPrompts()
	if got := c.AllowedPrompts(); len(got) != 0 {
		t.Errorf("clear should empty allowedPrompts; got %v", got)
	}
}

func TestModeFromStringLegacy(t *testing.T) {
	cases := map[string]Mode{
		"":                  ModeDefault,
		"default":           ModeDefault,
		"acceptEdits":       ModeAcceptEdits,
		"accept_edits":      ModeAcceptEdits,
		"plan":              ModePlan,
		"bypassPermissions": ModeBypass,
		"bypass":            ModeBypass,
		"ask":               ModeDefault,
		"auto_edit":         ModeAcceptEdits,
		"full_access":       ModeBypass,
		"garbage":           ModeDefault,
	}
	for in, want := range cases {
		if got := ModeFromString(in); got != want {
			t.Errorf("ModeFromString(%q)=%v, want %v", in, got, want)
		}
	}
}

func TestEnterPlanModeRemembersPrevious(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeAcceptEdits)
	prev := c.EnterPlanMode()
	if prev != ModeAcceptEdits {
		t.Errorf("EnterPlanMode should report previous mode; got %v", prev)
	}
	if c.Mode() != ModePlan {
		t.Errorf("mode not flipped; got %v", c.Mode())
	}
	if c.PrePlanMode() != ModeAcceptEdits {
		t.Errorf("prePlanMode not stored; got %v", c.PrePlanMode())
	}
}

func TestExitPlanModeRestoresPrevious(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeAcceptEdits)
	c.EnterPlanMode()
	restored := c.ExitPlanMode()
	if restored != ModeAcceptEdits {
		t.Errorf("ExitPlanMode should return restored mode; got %v", restored)
	}
	if c.Mode() != ModeAcceptEdits {
		t.Errorf("mode not restored; got %v", c.Mode())
	}
	if c.PrePlanMode() != "" {
		t.Errorf("prePlanMode should clear after exit; got %v", c.PrePlanMode())
	}
}

func TestExitPlanModeWithoutEnterFallsBackToDefault(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeAcceptEdits) // some non-plan mode
	// Skipping EnterPlanMode → prePlanMode is empty.
	got := c.ExitPlanMode()
	if got != ModeDefault {
		t.Errorf("ExitPlanMode without prePlanMode should restore to default; got %v", got)
	}
}

func TestEnterPlanModeNestedPreservesOriginal(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeAcceptEdits)
	c.EnterPlanMode()
	// Nested EnterPlanMode (model double-calls): prePlanMode must
	// keep the original mode, not collapse to plan.
	c.EnterPlanMode()
	if c.PrePlanMode() != ModeAcceptEdits {
		t.Errorf("nested enter should preserve outer prePlanMode; got %v", c.PrePlanMode())
	}
}

// TestPlanModeE2EDenyMatrix walks the full permission decision path
// across every common tool with plan mode active, asserting reads
// pass and writes are denied with the correct reason.
func TestPlanModeE2EDenyMatrix(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeAcceptEdits) // entering plan FROM acceptEdits
	c.EnterPlanMode()

	cases := []struct {
		name       string
		req        Request
		want       Decision
		wantReason string // substring of Reason.Detail
	}{
		{"Read passes", Request{Tool: "Read", IsReadOnly: true,
			Args: map[string]any{"path": "/x.go"}}, DecideAllow, "read-only tool"},
		{"Glob passes", Request{Tool: "Glob", IsReadOnly: true}, DecideAllow, "read-only"},
		{"Grep passes", Request{Tool: "Grep", IsReadOnly: true}, DecideAllow, "read-only"},
		{"Edit denied", Request{Tool: "Edit",
			Args: map[string]any{"path": "/x.go"}}, DecideDeny, "plan mode"},
		{"Write denied", Request{Tool: "Write",
			Args: map[string]any{"path": "/x.go"}}, DecideDeny, "plan mode"},
		{"Bash denied", Request{Tool: "Bash", IsDestructive: true,
			Args: map[string]any{"command": "rm -rf /"}}, DecideDeny, "plan mode"},
		{"NotebookEdit denied", Request{Tool: "NotebookEdit"}, DecideDeny, "plan mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, r := Decide(c, tc.req)
			if d != tc.want {
				t.Errorf("decision %v, want %v (reason=%+v)", d, tc.want, r)
			}
			if !strings.Contains(r.Detail, tc.wantReason) {
				t.Errorf("reason %q missing %q", r.Detail, tc.wantReason)
			}
		})
	}

	// Restore: now Edit must be allowed again because we restored to
	// acceptEdits.
	c.ExitPlanMode()
	if c.Mode() != ModeAcceptEdits {
		t.Fatalf("ExitPlanMode should restore acceptEdits; got %v", c.Mode())
	}
	d, _ := Decide(c, Request{Tool: "Edit", Args: map[string]any{"path": "/x.go"}})
	if d != DecideAllow {
		t.Errorf("after restore, Edit must be allowed; got %v", d)
	}
}

// TestPlanModeRespectsAllowRulesForReadOnly — even strict allow rules
// can't override plan mode's deny on writes (plan mode wins at step 2,
// before rule evaluation).
func TestPlanModeIgnoresAllowRulesForWrites(t *testing.T) {
	c := NewContext()
	c.ReplaceRules(SrcUserSettings, BehaviorAllow, []string{"Edit(./**)"})
	c.EnterPlanMode()

	d, r := Decide(c, Request{Tool: "Edit",
		Args: map[string]any{"path": "/x.go"}})
	if d != DecideDeny {
		t.Errorf("plan mode must override allow rules for writes; got %v reason=%+v", d, r)
	}
	if !strings.Contains(r.Detail, "plan mode") {
		t.Errorf("reason should cite plan mode; got %q", r.Detail)
	}
}

func TestPlanAttachmentSetClear(t *testing.T) {
	c := NewContext()
	if got := c.PlanAttachment(); got != "" {
		t.Errorf("zero-value attachment must be empty; got %q", got)
	}
	c.SetPlanAttachment("## Plan\n1. step")
	if got := c.PlanAttachment(); got != "## Plan\n1. step" {
		t.Errorf("attachment not stored: %q", got)
	}
	c.SetPlanAttachment("")
	if got := c.PlanAttachment(); got != "" {
		t.Errorf("clear failed; got %q", got)
	}
}

// ─── Additional working directories ───────────────────

func TestAdditionalDirectories_AddDedup(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcLocalSettings, []string{"/tmp/a", "/tmp/b", ""})
	c.AddDirectories(SrcSession, []string{"/tmp/a", "/tmp/c"}) // /tmp/a should NOT change source

	got := c.AdditionalDirectories()
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(got), got)
	}
	// sorted by path
	if got[0].Path != "/tmp/a" || got[1].Path != "/tmp/b" || got[2].Path != "/tmp/c" {
		t.Errorf("unsorted: %+v", got)
	}
	// /tmp/a keeps its first source
	if got[0].Source != SrcLocalSettings {
		t.Errorf("/tmp/a source should stay SrcLocalSettings on second add; got %s", got[0].Source)
	}
	if got[2].Source != SrcSession {
		t.Errorf("/tmp/c source should be SrcSession; got %s", got[2].Source)
	}
}

func TestAdditionalDirectories_Remove(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcSession, []string{"/tmp/a", "/tmp/b"})
	c.RemoveDirectories([]string{"/tmp/a", "/tmp/missing"})
	got := c.AdditionalDirectoryPaths()
	if len(got) != 1 || got[0] != "/tmp/b" {
		t.Errorf("after remove want [/tmp/b]; got %+v", got)
	}
	if _, ok := c.DirectorySource("/tmp/a"); ok {
		t.Errorf("removed dir should not have source")
	}
	if src, ok := c.DirectorySource("/tmp/b"); !ok || src != SrcSession {
		t.Errorf("/tmp/b should be SrcSession; got %s ok=%v", src, ok)
	}
}

func TestAdditionalDirectories_ObserverFiresOnAddAndRemove(t *testing.T) {
	c := NewContext()
	calls := 0
	c.OnDirectoriesChanged(func() { calls++ })

	c.AddDirectories(SrcSession, []string{"/tmp/a"})
	if calls != 1 {
		t.Errorf("add must fire observer; calls=%d", calls)
	}
	// duplicate add: no fire
	c.AddDirectories(SrcSession, []string{"/tmp/a"})
	if calls != 1 {
		t.Errorf("duplicate add must not fire; calls=%d", calls)
	}
	c.RemoveDirectories([]string{"/tmp/a"})
	if calls != 2 {
		t.Errorf("remove must fire; calls=%d", calls)
	}
	// remove of unknown: no fire
	c.RemoveDirectories([]string{"/tmp/missing"})
	if calls != 2 {
		t.Errorf("no-op remove must not fire; calls=%d", calls)
	}
}

func TestAdditionalDirectories_Concurrent(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcSession, []string{"/tmp/seed"})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			c.AddDirectories(SrcSession, []string{"/tmp/x"})
			c.RemoveDirectories([]string{"/tmp/x"})
		}
		close(done)
	}()
	for i := 0; i < 200; i++ {
		_ = c.AdditionalDirectoryPaths()
		_, _ = c.DirectorySource("/tmp/seed")
	}
	<-done
	// /tmp/seed must still be present
	if _, ok := c.DirectorySource("/tmp/seed"); !ok {
		t.Errorf("seed lost during concurrent ops")
	}
}

// ─── Working-dir gate ─────────────────────────────────

func TestDecide_ReadOutsideWorkingDir_AsksEvenForReadOnly(t *testing.T) {
	c := NewContext()
	c.SetOriginalCwd("/repo")
	d, r := Decide(c, Request{
		Tool: "Read", IsReadOnly: true,
		Args: map[string]any{"file_path": "/etc/passwd"},
	})
	if d != DecideAsk {
		t.Errorf("read outside cwd should ask; got %v reason=%+v", d, r)
	}
	if r.Kind != "workingDir" {
		t.Errorf("reason kind = %s, want workingDir", r.Kind)
	}
}

func TestDecide_ReadInsideCwd_AllowedReadOnly(t *testing.T) {
	c := NewContext()
	c.SetOriginalCwd("/repo")
	d, _ := Decide(c, Request{
		Tool: "Read", IsReadOnly: true,
		Args: map[string]any{"file_path": "/repo/src/x.go"},
	})
	if d != DecideAllow {
		t.Errorf("read inside cwd should allow; got %v", d)
	}
}

func TestDecide_ReadInExtraDir_Allowed(t *testing.T) {
	c := NewContext()
	c.SetOriginalCwd("/repo")
	c.AddDirectories(SrcSession, []string{"/tmp/proj"})
	d, _ := Decide(c, Request{
		Tool: "Read", IsReadOnly: true,
		Args: map[string]any{"file_path": "/tmp/proj/x.go"},
	})
	if d != DecideAllow {
		t.Errorf("read inside extra dir should allow; got %v", d)
	}
}

func TestDecide_WriteOutsideWorkingDir_Asks(t *testing.T) {
	c := NewContext()
	c.SetOriginalCwd("/repo")
	d, r := Decide(c, Request{
		Tool: "Edit",
		Args: map[string]any{"file_path": "/etc/passwd"},
	})
	if d != DecideAsk {
		t.Errorf("write outside cwd should ask; got %v reason=%+v", d, r)
	}
}

func TestDecide_WriteInsideExtraDir_AllowsWithAcceptEdits(t *testing.T) {
	c := NewContext()
	c.SetOriginalCwd("/repo")
	c.AddDirectories(SrcSession, []string{"/tmp/proj"})
	c.SetMode(ModeAcceptEdits)
	d, _ := Decide(c, Request{
		Tool: "Edit",
		Args: map[string]any{"file_path": "/tmp/proj/x.go"},
	})
	if d != DecideAllow {
		t.Errorf("acceptEdits + extra dir should allow; got %v", d)
	}
}

func TestDecide_NoOriginalCwd_FailOpen(t *testing.T) {
	// Headless / pre-wiring — no cwd set, no extras. Gate should
	// degrade to allow (it's not the last line of defense).
	c := NewContext()
	d, _ := Decide(c, Request{
		Tool: "Read", IsReadOnly: true,
		Args: map[string]any{"file_path": "/wherever"},
	})
	if d != DecideAllow {
		t.Errorf("no cwd → should allow read-only; got %v", d)
	}
}

func TestDecide_DenyRuleStillVetoesWorkingDirCheck(t *testing.T) {
	c := NewContext()
	c.SetOriginalCwd("/repo")
	c.AddRules(SrcSession, BehaviorDeny, []string{"Read(/repo/secret)"})
	d, _ := Decide(c, Request{
		Tool: "Read", IsReadOnly: true,
		Args: map[string]any{"file_path": "/repo/secret"},
	})
	if d != DecideDeny {
		t.Errorf("deny rule must beat working-dir gate; got %v", d)
	}
}
