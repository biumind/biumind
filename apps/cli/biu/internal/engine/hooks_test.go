package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// writeShellHook drops a tiny shell script into a temp dir and
// returns its path. Skips on Windows where /bin/sh isn't available.
func writeShellHook(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook tests need /bin/sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnginePreToolUseHookBlocks(t *testing.T) {
	script := writeShellHook(t, `cat > /dev/null
printf '%s' '{"block":true,"reason":"never run rm"}'
`)

	prov := &scriptedProvider{scripts: [][]StreamFrame{
		toolUseTurn("calling", "tu_1", "Bash", `{"command":"rm -rf /"}`),
		textTurn("done"),
	}}
	st := state.New()
	reg := NewRegistry()
	bash := &fakeTool{name: "Bash", concurrencySafe: false}
	reg.Register(bash)

	hookReg := hooks.NewRegistry()
	hookReg.Add("user", map[string][]json.RawMessage{
		"PreToolUse": {json.RawMessage(`[{"matcher":"Bash","hooks":[{"type":"command","command":"` + script + `"}]}]`)},
	})

	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true, Hooks: hookReg,
	})
	events := drainAll(eng.Submit(context.Background(), "wipe"))

	if bash.calls != 0 {
		t.Errorf("PreToolUse block must skip tool; bash ran %d times", bash.calls)
	}
	if !hasEvent(events, func(r *ToolUseResultEvent) bool {
		return r.Result.IsError && r.Name == "Bash"
	}) {
		t.Errorf("expected soft-error tool result for blocked call")
	}
}

func TestEngineUserPromptSubmitHookRewrites(t *testing.T) {
	script := writeShellHook(t, `cat > /dev/null
printf '%s' '{"replacePrompt":"REWRITTEN"}'
`)

	prov := &scriptedProvider{scripts: [][]StreamFrame{textTurn("ok")}}
	st := state.New()
	reg := NewRegistry()

	hookReg := hooks.NewRegistry()
	hookReg.Add("user", map[string][]json.RawMessage{
		"UserPromptSubmit": {json.RawMessage(`[{"hooks":[{"type":"command","command":"` + script + `"}]}]`)},
	})

	eng, _ := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true, Hooks: hookReg,
	})
	drainAll(eng.Submit(context.Background(), "ORIGINAL"))

	// The user message in state should be the rewritten one.
	snap := st.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected at least one message")
	}
	user := snap[0]
	if user.Role != state.RoleUser {
		t.Fatalf("first message not user: %+v", user)
	}
	got := ""
	for _, b := range user.Content {
		if b.Type == state.ContentText {
			got = b.Text
		}
	}
	if got != "REWRITTEN" {
		t.Errorf("prompt rewrite failed: got %q", got)
	}
}
