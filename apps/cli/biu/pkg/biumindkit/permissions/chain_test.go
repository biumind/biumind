package permissions

import "testing"

func TestAutoModeApprovesDestructive(t *testing.T) {
	ctx := NewContext()
	ctx.SetMode(ModeAuto)
	d, reason := Decide(ctx, Request{Tool: "Bash", IsDestructive: true, Args: map[string]any{"command": "rm -rf /tmp/x"}})
	if d != DecideAllow {
		t.Fatalf("auto mode should approve destructive calls; got %v (%s)", d, reason.Detail)
	}
	if reason.Kind != "mode" {
		t.Errorf("expected mode reason, got %q", reason.Kind)
	}
}

func TestAutoModeDenyRuleStillWins(t *testing.T) {
	ctx := NewContext()
	ctx.SetMode(ModeAuto)
	ctx.AddRules(SrcUserSettings, BehaviorDeny, []string{"Bash(rm:*)"})
	d, reason := Decide(ctx, Request{Tool: "Bash", IsDestructive: true, Args: map[string]any{"command": "rm -rf /tmp/x"}})
	if d != DecideDeny {
		t.Fatalf("deny rule must veto auto mode; got %v", d)
	}
	if reason.Kind != "rule" {
		t.Errorf("expected rule reason, got %q", reason.Kind)
	}
}

func TestAutoModePlanUnaffected(t *testing.T) {
	ctx := NewContext()
	ctx.SetMode(ModePlan)
	if d, _ := Decide(ctx, Request{Tool: "Bash", IsDestructive: true, Args: map[string]any{"command": "ls"}}); d != DecideDeny {
		t.Errorf("plan mode still denies writes; got %v", d)
	}
}

func TestDenyRuleBeatsSessionGrant(t *testing.T) {
	ctx := NewContext()
	ctx.Grant(SessionGrantKey("Bash", map[string]any{"command": "rm -rf /tmp/x"}))
	ctx.AddRules(SrcUserSettings, BehaviorDeny, []string{"Bash(rm:*)"})
	d, _ := Decide(ctx, Request{Tool: "Bash", IsDestructive: true, Args: map[string]any{"command": "rm -rf /tmp/x"}})
	if d != DecideDeny {
		t.Errorf("deny rule must outrank session grant; got %v", d)
	}
}

func TestBypassStillSkipsDenyRules(t *testing.T) {
	ctx := NewContext()
	ctx.SetMode(ModeBypass)
	ctx.AddRules(SrcUserSettings, BehaviorDeny, []string{"Bash(rm:*)"})
	d, _ := Decide(ctx, Request{Tool: "Bash", IsDestructive: true, Args: map[string]any{"command": "rm -rf /tmp/x"}})
	if d != DecideAllow {
		t.Errorf("bypass (legacy yolo) skips rule evaluation; got %v", d)
	}
}

func TestModeFromStringAuto(t *testing.T) {
	if got := ModeFromString("auto"); got != ModeAuto {
		t.Errorf("ModeFromString(auto) = %v", got)
	}
	if !ModeAuto.IsValid() {
		t.Error("ModeAuto should be valid")
	}
	if ModeAuto.ShortTitle() != "Auto" {
		t.Errorf("ShortTitle = %q", ModeAuto.ShortTitle())
	}
}
