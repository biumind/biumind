// Pins the Reason.Policy stamping in Decide (chain.go): audit
// surfaces branch on the policy identity, not the human-readable
// Detail text, so the stamp must stay stable.

package permissions

import "testing"

func TestDecideStampsPolicyName(t *testing.T) {
	// auto-mode-approve
	auto := NewContext()
	auto.SetMode(ModeAuto)
	dec, r := Decide(auto, Request{Tool: "Bash"})
	if dec != DecideAllow || r.Policy != "auto-mode-approve" {
		t.Errorf("auto allow: dec=%v Policy=%q", dec, r.Policy)
	}

	// dangerous-command guard
	guarded := NewContext()
	guarded.SetMode(ModeAuto)
	dec, r = Decide(guarded, Request{Tool: "Bash", Warnings: []string{"sudo"}})
	if dec != DecideAsk || r.Policy != "dangerous-command-ask" {
		t.Errorf("guard ask: dec=%v Policy=%q", dec, r.Policy)
	}

	// sensitive-path guard
	dec, r = Decide(guarded, Request{
		Tool: "Write", Args: map[string]any{"file_path": "/repo/.env"},
	})
	if dec != DecideAsk || r.Policy != "sensitive-path-ask" {
		t.Errorf("sensitive path ask: dec=%v Policy=%q", dec, r.Policy)
	}

	// fallback (manual mode, destructive)
	dec, r = Decide(NewContext(), Request{Tool: "Bash", IsDestructive: true})
	if dec != DecideAsk || r.Policy != "fallback-ask" {
		t.Errorf("fallback ask: dec=%v Policy=%q", dec, r.Policy)
	}

	// deny rule
	deny := NewContext()
	deny.AddRules(SrcUserSettings, BehaviorDeny, []string{"Bash(rm:*)"})
	dec, r = Decide(deny, Request{
		Tool: "Bash", Args: map[string]any{"command": "rm -rf /"},
	})
	if dec != DecideDeny || r.Policy != "user-configured-deny" {
		t.Errorf("deny: dec=%v Policy=%q", dec, r.Policy)
	}
}
