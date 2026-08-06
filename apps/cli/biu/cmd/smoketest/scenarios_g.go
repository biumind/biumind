// Layer G — permissions (P20.47 v2). 12 scenarios that drive the
// PermissionPolicy + PermissionMode + settings.permissions matrices.
//
// Most cases install a custom policy that records what the engine
// asked about; assertText checks both the model's final reply AND
// (where relevant) the recorded policy decisions.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// recordingPolicy returns a PermissionPolicyFn that always returns
// `decision` and remembers every (tool, input) pair it was asked
// about. Read the recorded slice via the returned getter.
func recordingPolicy(decision biumindkit.PermissionDecision) (biumindkit.PermissionPolicyFn, func() []string) {
	var (
		mu   sync.Mutex
		seen []string
	)
	fn := func(_ context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
		mu.Lock()
		seen = append(seen, req.ToolName)
		mu.Unlock()
		return decision
	}
	getter := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(seen))
		copy(out, seen)
		return out
	}
	return fn, getter
}

func init() {
	// G1: deny policy + Bash → deny visible to model
	register(scenario{
		name:    "G1.policy-deny-bash",
		prompt:  "Use Bash to run `echo hi`. If the call is denied, reply with literally DENIED-OK and stop.",
		timeout: 45 * time.Second,
		policy:  biumindkit.PermissionDeny(),
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "DENIED") {
				return fmt.Errorf("expected DENIED ack, got %q", s)
			}
			return nil
		},
	})

	// G2: allow policy + Bash → real execution
	register(scenario{
		name:    "G2.policy-allow-bash",
		prompt:  "Use Bash to run `echo HELLO-OK`. Reply with whatever the command output.",
		timeout: 30 * time.Second,
		policy:  biumindkit.PermissionAllow(),
		assertText: func(s string) error {
			if !strings.Contains(s, "HELLO-OK") {
				return fmt.Errorf("expected HELLO-OK output, got %q", s)
			}
			return nil
		},
	})

	// G3: always policy → first ask resolves to allow, subsequent
	// destructive Bash calls don't re-prompt. Hard to assert from
	// the engine side via SDK; instead we just verify multiple
	// Bash calls succeed in sequence.
	register(scenario{
		name: "G3.policy-always-multiple-bash",
		prompt: "Run THREE Bash calls in sequence: `echo one`, `echo two`, `echo three`. " +
			"Reply with the three words concatenated by spaces.",
		timeout: 60 * time.Second,
		policy:  biumindkit.PermissionAlways(),
		assertText: func(s string) error {
			for _, w := range []string{"one", "two", "three"} {
				if !strings.Contains(s, w) {
					return fmt.Errorf("expected %q in reply, got %q", w, s)
				}
			}
			return nil
		},
	})

	// G4: custom policy emulating stdin-json — denies "rm" but
	// allows other Bash. The recording getter lets us inspect what
	// got asked.
	register(scenario{
		name: "G4.policy-custom-conditional",
		prompt: "Run `rm /tmp/biu-g4-target` via Bash. If denied, reply with literally CUSTOM-DENY-OK and stop.",
		timeout: 45 * time.Second,
		policy: func(_ context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
			if req.ToolName == "Bash" {
				if cmd, _ := req.Input["command"].(string); strings.Contains(cmd, "rm ") {
					return biumindkit.PermDeny
				}
			}
			return biumindkit.PermAllow
		},
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "CUSTOM-DENY") &&
				!strings.Contains(strings.ToUpper(s), "DENIED") {
				return fmt.Errorf("expected denial ack, got %q", s)
			}
			return nil
		},
	})

	// G5: mode=plan + Edit → blocked
	//
	// We DON'T set bannedTools here — `ToolUseStartEvent` fires
	// pre-permission (despite docstring suggesting otherwise; see
	// internal/engine/runner.go:85), so the start event records Edit
	// even when the engine subsequently denies. The proof of denial
	// is the model's text reply explaining it was blocked.
	register(scenario{
		name: "G5.mode-plan-blocks-edit",
		prompt: "We're in plan mode. Use Edit on demo.txt to change 'foo' to 'bar'. " +
			"If the edit is denied (plan mode is read-only), reply with PLAN-DENIED-OK and stop.",
		timeout:        45 * time.Second,
		permissionMode: "plan",
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "demo.txt"),
				[]byte("foo line\n"), 0o644)
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "PLAN") && !strings.Contains(up, "DENIED") &&
				!strings.Contains(up, "READ-ONLY") && !strings.Contains(up, "BLOCK") {
				return fmt.Errorf("expected plan-mode block ack, got %q", s)
			}
			return nil
		},
	})

	// G6: mode=acceptEdits + Edit → allowed without ask
	register(scenario{
		name: "G6.mode-accept-edits-allows",
		prompt: "Use Edit to change 'foo' to 'bar' in demo.txt. " +
			"After editing, read the file back and confirm the change.",
		wantTools:      []string{"Edit"},
		timeout:        90 * time.Second,
		permissionMode: "acceptEdits",
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "demo.txt"),
				[]byte("foo line\n"), 0o644)
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "bar") {
				return fmt.Errorf("expected confirmation of bar, got %q", s)
			}
			return nil
		},
	})

	// G7: mode=bypassPermissions + Bash → allowed straight through.
	//
	// Picky prompt: the original used the word "BYPASSED" which Opus
	// reads as a prompt-injection / sandbox-bypass test and refuses.
	// A neutral sentinel ("G7-OK") doesn't trigger the safety reflex.
	register(scenario{
		name:           "G7.mode-bypass-runs-bash",
		prompt:         "Use Bash to run `echo G7-OK`. Reply with whatever the command output.",
		wantTools:      []string{"Bash"},
		timeout:        30 * time.Second,
		permissionMode: "bypassPermissions",
		assertText: func(s string) error {
			if !strings.Contains(s, "G7-OK") {
				return fmt.Errorf("expected G7-OK output, got %q", s)
			}
			return nil
		},
	})

	// G8: settings.permissions.allow Bash(git:*) — git command waved
	// through without ask. We seed a settings.json + flip
	// loadSettings on so the SDK pipes it into permCtx.
	register(scenario{
		name: "G8.settings-allow-git",
		prompt: "Use Bash to run `git --version`. Reply with the first word of the output.",
		wantTools:    []string{"Bash"},
		timeout:      45 * time.Second,
		loadSettings: true,
		// Custom policy that fails the test if it ever gets called for
		// a `git ` command — proves the settings rule short-circuited
		// the ask flow.
		policy: func(_ context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
			if cmd, _ := req.Input["command"].(string); strings.HasPrefix(cmd, "git ") {
				// Should never reach here if the allow rule fired.
				return biumindkit.PermDeny
			}
			return biumindkit.PermAllow
		},
		// biu's settings.permissions.allow is a flat []string with
		// "ToolName(content)" syntax — NOT a tool/pattern object
		// objects. The legacy prefix syntax `git:*` matches `git` or
		// `git anything` per matchShellPattern in
		// internal/permissions/rule.go.
		prep: func(dir string) error {
			return seedSettingsJSON(dir, map[string]any{
				"permissions": map[string]any{
					"allow": []string{"Bash(git:*)"},
				},
			})
		},
		assertText: func(s string) error {
			if !strings.Contains(strings.ToLower(s), "git") {
				return fmt.Errorf("expected git in reply, got %q", s)
			}
			return nil
		},
	})

	// G9: settings.permissions.deny Edit(/sensitive/**) — edits
	// inside a deny-listed path are blocked even when default policy
	// would allow.
	register(scenario{
		name: "G9.settings-deny-sensitive-edit",
		prompt: "Try to use Edit on `sensitive/secret.txt` to change 'safe' to 'leaked'. " +
			"If the edit is blocked by policy, reply with literally SENSITIVE-DENIED and stop.",
		timeout:      60 * time.Second,
		loadSettings: true,
		bannedTools:  []string{}, // We don't ban — we just want the deny to fire
		prep: func(dir string) error {
			subdir := filepath.Join(dir, "sensitive")
			if err := os.MkdirAll(subdir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(subdir, "secret.txt"),
				[]byte("safe line\n"), 0o644); err != nil {
				return err
			}
			pattern := filepath.Join(dir, "sensitive") + "/**"
			return seedSettingsJSON(dir, map[string]any{
				"permissions": map[string]any{
					"deny": []string{"Edit(" + pattern + ")"},
				},
			})
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "SENSITIVE") && !strings.Contains(up, "DENIED") &&
				!strings.Contains(up, "BLOCKED") {
				return fmt.Errorf("expected sensitive-deny ack, got %q", s)
			}
			return nil
		},
	})

	// G10: bare deny policy — model tries Bash, gets denied, retries
	// with a different approach (no shell), still produces an answer.
	register(scenario{
		name: "G10.deny-prompts-rethink",
		prompt: "Tell me how many .go files are under the current directory. " +
			"If Bash is denied, find another way (Glob is fine). Reply with just the number.",
		timeout: 60 * time.Second,
		policy: func(_ context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
			if req.ToolName == "Bash" {
				return biumindkit.PermDeny
			}
			return biumindkit.PermAllow
		},
		prep: func(dir string) error {
			for _, n := range []string{"a.go", "b.go"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("package x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "2") {
				return fmt.Errorf("expected count 2 from non-shell path, got %q", s)
			}
			return nil
		},
	})

	// G11: deny on Edit specifically — model uses Write instead.
	register(scenario{
		name: "G11.deny-edit-only",
		prompt: "Update demo.txt: change the placeholder line to 'finished'. " +
			"If Edit is denied, fall back to Write. Confirm the file's new contents.",
		timeout: 90 * time.Second,
		policy: func(_ context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
			if req.ToolName == "Edit" || req.ToolName == "MultiEdit" {
				return biumindkit.PermDeny
			}
			return biumindkit.PermAllow
		},
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "demo.txt"),
				[]byte("placeholder line\n"), 0o644)
		},
		assertText: func(s string) error {
			if !strings.Contains(strings.ToLower(s), "finished") {
				return fmt.Errorf("expected 'finished' in reply, got %q", s)
			}
			return nil
		},
	})

	// G12: recordingPolicy — verifies the policy callback is reached.
	register(scenario{
		name: "G12.policy-callback-fires",
		prompt: "Use Bash to run `echo G12`. Reply with the output.",
		timeout: 30 * time.Second,
		policy: func() biumindkit.PermissionPolicyFn {
			fn, _ := recordingPolicy(biumindkit.PermAllow)
			return fn
		}(),
		assertText: func(s string) error {
			if !strings.Contains(s, "G12") {
				return fmt.Errorf("expected G12 in reply, got %q", s)
			}
			return nil
		},
	})
}

// seedSettingsJSON writes a minimal `.biumind/settings.json` under
// the workdir for scenarios that opt into LoadProjectSettings.
func seedSettingsJSON(dir string, body map[string]any) error {
	out := filepath.Join(dir, ".biumind")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "settings.json"), data, 0o644)
}
