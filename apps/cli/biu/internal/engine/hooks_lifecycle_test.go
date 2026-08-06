// Tests for the SessionStart / SessionEnd / SubagentStop hook events
// added in the P20.13 audit.
//
// We use real shell hooks (`touch <tempfile>`) and check for the
// side-effect file rather than instrumenting hooks.Run — keeps the
// tests close to what users actually configure in settings.json.
//
// Important: each test puts its marker file under t.TempDir() so
// runs are hermetic and parallel-safe.

package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/internal/tools/orchestration"
)

// markerHooks builds a Registry whose hook for every passed event
// runs `touch <markerPath>` — gives a single file we can stat to
// confirm the hook ran.
func markerHooks(t *testing.T, markerPath string, events ...hooks.Event) *hooks.Registry {
	t.Helper()
	reg := hooks.NewRegistry()
	command, _ := json.Marshal("touch " + markerPath)
	raw := json.RawMessage(`[{"hooks":[{"type":"command","command":` + string(command) + `}]}]`)
	out := map[string][]json.RawMessage{}
	for _, e := range events {
		out[string(e)] = []json.RawMessage{raw}
	}
	reg.Add("test", out)
	return reg
}

// waitForFile polls every 20 ms up to deadline for the file at p to
// exist. Returns true on success.
func waitForFile(p string, deadline time.Duration) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err := os.Stat(p)
	return err == nil
}

// ─── tests ─────────────────────────────────────────────

// New() must NOT auto-fire SessionStart — only an explicit
// FireSessionStart triggers the hook. Locks the design so tests
// constructing ephemeral engines don't spawn user shells.
func TestSessionStartDoesNotFireFromNew(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "session-start")
	reg := markerHooks(t, marker, hooks.EventSessionStart)
	_, err := engine.New(engine.Options{
		State: state.New(), Tools: engine.NewRegistry(),
		Provider: &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}},
		Model:    "test",
		Hooks:    reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Brief wait so any accidental fire would have a chance to land.
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("SessionStart should not fire from engine.New()")
	}
}

func TestSessionStartFiresOnExplicitCall(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "session-start")
	reg := markerHooks(t, marker, hooks.EventSessionStart)
	eng, err := engine.New(engine.Options{
		State: state.New(), Tools: engine.NewRegistry(),
		Provider: &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}},
		Model:    "test",
		Hooks:    reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.FireSessionStart()
	if !waitForFile(marker, time.Second) {
		t.Errorf("SessionStart hook never ran after FireSessionStart()")
	}
}

func TestSessionStartIsIdempotent(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "session-start")
	reg := markerHooks(t, marker, hooks.EventSessionStart)
	eng, _ := engine.New(engine.Options{
		State: state.New(), Tools: engine.NewRegistry(),
		Provider: &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}},
		Model:    "test",
		Hooks:    reg,
	})
	eng.FireSessionStart()
	if !waitForFile(marker, time.Second) {
		t.Fatal("first FireSessionStart should run the hook")
	}
	// Re-fire is a sync.Once no-op. We can't directly observe "did
	// not run" without instrumenting hooks.Run; what we test is
	// that no error / panic propagates.
	eng.FireSessionStart() // must not panic
	eng.FireSessionStart() // still must not panic
}

func TestSessionEndFiresOnClose(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "session-end")
	reg := markerHooks(t, marker, hooks.EventSessionEnd)
	eng, err := engine.New(engine.Options{
		State: state.New(), Tools: engine.NewRegistry(),
		Provider: &scripted{turns: [][]engine.StreamFrame{textTurn("ok")}},
		Model:    "test",
		Hooks:    reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("SessionEnd should not fire before Close()")
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if !waitForFile(marker, time.Second) {
		t.Errorf("SessionEnd hook never ran after Close()")
	}
	// Second Close is the sync.Once no-op — must not error.
	if err := eng.Close(); err != nil {
		t.Errorf("second Close should not error; got %v", err)
	}
}

// SubagentStop fires after the spawner's child engine drains its
// event channel. Verified via a real CodeReview dispatch through
// AgentTool.
func TestSubagentStopFiresAfterDispatch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "subagent-stop")
	reg := markerHooks(t, marker, hooks.EventSubagentStop)

	registry, err := agents.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	prov := &codeReviewCaptureProvider{
		parentScript: [][]engine.StreamFrame{
			toolUseTurn("u1", "Agent",
				`{"subagent_type":"CodeReview","prompt":"review"}`),
			textTurn("done"),
		},
		childScript: textTurn("**Summary:** clean."),
	}

	parentReg := engine.NewRegistry()
	registerCatalog(parentReg, []string{"Read", "Glob", "Grep", "Bash", "WebFetch"})
	parentReg.Register(orchestration.AgentTool{Registry: registry})

	eng, _ := engine.New(engine.Options{
		State: state.New(), Tools: parentReg, Provider: prov, Model: "test",
		Hooks:             reg,
		BypassPermissions: true,
		MaxToolTurns:      6,
	})
	drainAll(eng.Submit(context.Background(), "review please"))

	if !waitForFile(marker, 2*time.Second) {
		t.Errorf("SubagentStop hook never ran after CodeReview dispatch")
	}
}

// SubagentStop must NOT fire when no sub-agent is dispatched —
// guards against accidental coupling with the parent's Stop hook.
func TestSubagentStopDoesNotFireWithoutSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "subagent-stop")
	reg := markerHooks(t, marker, hooks.EventSubagentStop)
	prov := &scripted{turns: [][]engine.StreamFrame{textTurn("just text")}}
	eng, _ := engine.New(engine.Options{
		State: state.New(), Tools: engine.NewRegistry(),
		Provider: prov, Model: "test",
		Hooks:             reg,
		BypassPermissions: true,
	})
	drainAll(eng.Submit(context.Background(), "no spawn"))
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("SubagentStop fired without a sub-agent dispatch")
	}
}

// AllEvents must include the new SubagentStop so config validators
// recognise it and don't print "unknown event" warnings.
func TestAllEventsIncludesSubagentStop(t *testing.T) {
	found := false
	for _, e := range hooks.AllEvents {
		if e == hooks.EventSubagentStop {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AllEvents missing SubagentStop; config validators won't recognise it")
	}
	if !hooks.IsValid("SubagentStop") {
		t.Errorf("IsValid should accept SubagentStop")
	}
}
