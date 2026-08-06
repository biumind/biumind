package permissions

import (
	"strings"
	"testing"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

func TestApplyPermissionUpdate_AddDirectories(t *testing.T) {
	c := NewContext()
	calls := 0
	c.OnDirectoriesChanged(func() { calls++ })

	err := ApplyPermissionUpdate(c, &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{"/tmp/a", "/tmp/b"},
		Destination: sdkproto.PermissionDestSession,
	})
	if err != nil {
		t.Fatalf("apply addDirectories: %v", err)
	}
	if got := c.AdditionalDirectoryPaths(); len(got) != 2 || got[0] != "/tmp/a" || got[1] != "/tmp/b" {
		t.Errorf("after add want [/tmp/a /tmp/b]; got %+v", got)
	}
	src, ok := c.DirectorySource("/tmp/a")
	if !ok || src != SrcSession {
		t.Errorf("/tmp/a source should be SrcSession; got %s ok=%v", src, ok)
	}
	if calls != 1 {
		t.Errorf("observer should fire once; calls=%d", calls)
	}
}

func TestApplyPermissionUpdate_RemoveDirectories(t *testing.T) {
	c := NewContext()
	c.AddDirectories(SrcLocalSettings, []string{"/tmp/a", "/tmp/b"})

	err := ApplyPermissionUpdate(c, &sdkproto.RemoveDirectories{
		Type:        sdkproto.PermissionUpdateRemoveDirectories,
		Directories: []string{"/tmp/a"},
	})
	if err != nil {
		t.Fatalf("apply removeDirectories: %v", err)
	}
	got := c.AdditionalDirectoryPaths()
	if len(got) != 1 || got[0] != "/tmp/b" {
		t.Errorf("after remove want [/tmp/b]; got %+v", got)
	}
}

func TestApplyPermissionUpdate_DestinationToSource(t *testing.T) {
	cases := []struct {
		dest string
		want Source
	}{
		{sdkproto.PermissionDestSession, SrcSession},
		{sdkproto.PermissionDestLocalSettings, SrcLocalSettings},
		{sdkproto.PermissionDestUserSettings, SrcUserSettings},
		{sdkproto.PermissionDestProjectSettings, SrcProjectSettings},
		{sdkproto.PermissionDestCliArg, SrcCLIArg},
		{"unknown-thing", SrcSession}, // safe default
		{"", SrcSession},
	}
	for _, tc := range cases {
		got := SourceFromDestination(tc.dest)
		if got != tc.want {
			t.Errorf("dest=%q → %s, want %s", tc.dest, got, tc.want)
		}
	}
}

func TestApplyPermissionUpdate_AddRules(t *testing.T) {
	c := NewContext()
	err := ApplyPermissionUpdate(c, &sdkproto.AddRules{
		Type: sdkproto.PermissionUpdateAddRules,
		Rules: []sdkproto.PermissionRuleValue{
			{ToolName: "Bash", RuleContent: "npm install"},
			{ToolName: "Edit"},
		},
		Behavior:    sdkproto.PermissionAllow,
		Destination: sdkproto.PermissionDestSession,
	})
	if err != nil {
		t.Fatalf("apply addRules: %v", err)
	}
	rules := c.AllRules(BehaviorAllow)
	if len(rules) != 2 {
		t.Fatalf("want 2 allow rules, got %d", len(rules))
	}
	for _, r := range rules {
		if r.Source != SrcSession {
			t.Errorf("rule source must be SrcSession; got %s", r.Source)
		}
	}
}

func TestApplyPermissionUpdate_ReplaceRules(t *testing.T) {
	c := NewContext()
	c.AddRules(SrcSession, BehaviorAllow, []string{"Read", "Glob"})
	err := ApplyPermissionUpdate(c, &sdkproto.ReplaceRules{
		Type: sdkproto.PermissionUpdateReplaceRules,
		Rules: []sdkproto.PermissionRuleValue{
			{ToolName: "Bash", RuleContent: "git:*"},
		},
		Behavior:    sdkproto.PermissionAllow,
		Destination: sdkproto.PermissionDestSession,
	})
	if err != nil {
		t.Fatalf("apply replaceRules: %v", err)
	}
	rules := c.AllRules(BehaviorAllow)
	// only the new rule should remain in the SrcSession bucket; other sources untouched
	sessionRules := 0
	for _, r := range rules {
		if r.Source == SrcSession {
			sessionRules++
			if r.Value.ToolName != "Bash" {
				t.Errorf("expected Bash, got %s", r.Value.ToolName)
			}
		}
	}
	if sessionRules != 1 {
		t.Errorf("session rule count after replace = %d, want 1", sessionRules)
	}
}

func TestApplyPermissionUpdate_RemoveRules(t *testing.T) {
	c := NewContext()
	c.AddRules(SrcSession, BehaviorAllow, []string{"Bash(git:*)", "Read", "Glob"})
	err := ApplyPermissionUpdate(c, &sdkproto.RemoveRules{
		Type: sdkproto.PermissionUpdateRemoveRules,
		Rules: []sdkproto.PermissionRuleValue{
			{ToolName: "Bash", RuleContent: "git:*"},
		},
		Behavior:    sdkproto.PermissionAllow,
		Destination: sdkproto.PermissionDestSession,
	})
	if err != nil {
		t.Fatalf("apply removeRules: %v", err)
	}
	rules := c.AllRules(BehaviorAllow)
	for _, r := range rules {
		if r.Source == SrcSession && r.Value.ToolName == "Bash" {
			t.Errorf("Bash(git:*) should have been removed; rules=%+v", rules)
		}
	}
}

func TestApplyPermissionUpdate_SetMode(t *testing.T) {
	c := NewContext()
	err := ApplyPermissionUpdate(c, &sdkproto.SetModeUpdate{
		Type:        sdkproto.PermissionUpdateSetMode,
		Mode:        sdkproto.PermissionModeAcceptEdits,
		Destination: sdkproto.PermissionDestSession,
	})
	if err != nil {
		t.Fatalf("apply setMode: %v", err)
	}
	if c.Mode() != ModeAcceptEdits {
		t.Errorf("mode after setMode = %s, want acceptEdits", c.Mode())
	}
}

func TestApplyPermissionUpdate_NilGuards(t *testing.T) {
	if err := ApplyPermissionUpdate(nil, &sdkproto.AddDirectories{}); err == nil {
		t.Errorf("nil ctx should error")
	}
	if err := ApplyPermissionUpdate(NewContext(), nil); err == nil {
		t.Errorf("nil update should error")
	}
}

func TestApplyPermissionUpdates_BatchStopsOnError(t *testing.T) {
	c := NewContext()
	updates := []sdkproto.PermissionUpdate{
		&sdkproto.AddDirectories{
			Type: sdkproto.PermissionUpdateAddDirectories, Directories: []string{"/tmp/a"},
			Destination: sdkproto.PermissionDestSession,
		},
		nil, // forces error mid-batch
	}
	err := ApplyPermissionUpdates(c, updates)
	if err == nil || !strings.Contains(err.Error(), "[1]") {
		t.Errorf("batch should error at index 1; got %v", err)
	}
	// First update must still have applied
	if got := c.AdditionalDirectoryPaths(); len(got) != 1 {
		t.Errorf("first update should have applied before error; got %+v", got)
	}
}
