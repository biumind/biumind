// Verifies BashTool also surfaces the destructive-pattern warning
// inside its result body (post-approval). This is the
// self-audit channel — the model sees the warning in its own
// context after it ran, not just the user in the ask dialog. Pairs
// with the runner-side enrichment covered in engine/warner_test.go.

package web

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

func TestBashBodyAnnotatesDestructiveWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := BashTool{}
	// Use a harmless `rm -rf` target — a non-existent path under a
	// fresh temp dir produces exit 0 from BSD rm with -f, leaving
	// just the warning we're after.
	dir := t.TempDir()
	out, _ := tool.Call(context.Background(), map[string]any{
		"command": "rm -rf " + dir + "/nope",
	}, &engine.ToolEnv{})
	body := flattenBg(out)
	if !strings.Contains(body, "[warning]") {
		t.Errorf("expected [warning] line in body for rm -rf; got:\n%s", body)
	}
	if !strings.Contains(body, "may recursively force-remove files") {
		t.Errorf("expected destructive-warning text; got:\n%s", body)
	}
}

// Innocuous commands should NOT carry a [warning] line — false
// positives would teach the model to ignore them.
func TestBashBodyNoWarningForSafeCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell only")
	}
	tool := BashTool{}
	out, _ := tool.Call(context.Background(), map[string]any{
		"command": "echo hello",
	}, &engine.ToolEnv{})
	body := flattenBg(out)
	if strings.Contains(body, "[warning]") {
		t.Errorf("safe command should not produce [warning]; got:\n%s", body)
	}
}
