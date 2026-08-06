package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// stubAsyncSpawner satisfies engine.AsyncSpawner for tests. Records
// every SpawnAsync call so assertions can inspect what got passed.
type stubAsyncSpawner struct {
	calls    []engine.AgentSpawnRequest
	handle   engine.TeammateHandle
	spawnErr error
}

func (s *stubAsyncSpawner) Spawn(_ context.Context, _ engine.AgentSpawnRequest) (*engine.AgentSpawnResult, error) {
	return &engine.AgentSpawnResult{}, nil
}

func (s *stubAsyncSpawner) SpawnAsync(_ context.Context, req engine.AgentSpawnRequest) (engine.TeammateHandle, error) {
	s.calls = append(s.calls, req)
	if s.spawnErr != nil {
		return engine.TeammateHandle{}, s.spawnErr
	}
	if s.handle.ID == "" {
		s.handle = engine.TeammateHandle{
			ID: "agent-test", AgentType: req.AgentType,
			Description: req.Description, Started: time.Now(),
		}
	}
	return s.handle, nil
}

// TestAgentBackground_BasicSpawn — happy path: returns handle id +
// agent type in a friendly text result.
func TestAgentBackground_BasicSpawn(t *testing.T) {
	spawner := &stubAsyncSpawner{}
	env := &engine.ToolEnv{Spawner: spawner}
	out, err := AgentBackgroundTool{}.Call(context.Background(), map[string]any{
		"prompt":      "research auth",
		"description": "Find JWT validation",
	}, env)
	if err != nil || out.IsError {
		t.Fatalf("unexpected error: err=%v out=%+v", err, out)
	}
	body := flatten(out)
	for _, must := range []string{"agent-test", "general-purpose", "Find JWT validation"} {
		if !strings.Contains(body, must) {
			t.Errorf("result missing %q:\n%s", must, body)
		}
	}
	if len(spawner.calls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d", len(spawner.calls))
	}
	got := spawner.calls[0]
	if got.Prompt != "research auth" {
		t.Errorf("prompt=%q, want 'research auth'", got.Prompt)
	}
	if got.AgentType != "general-purpose" {
		t.Errorf("default agent type should be general-purpose; got %q", got.AgentType)
	}
}

// TestAgentBackground_AgentTypeForwarded — explicit subagent_type
// flows into the spawn request.
func TestAgentBackground_AgentTypeForwarded(t *testing.T) {
	spawner := &stubAsyncSpawner{}
	env := &engine.ToolEnv{Spawner: spawner}
	_, _ = AgentBackgroundTool{}.Call(context.Background(), map[string]any{
		"prompt":        "x",
		"subagent_type": "explore",
	}, env)
	if len(spawner.calls) != 1 || spawner.calls[0].AgentType != "explore" {
		t.Errorf("agent type lost: %+v", spawner.calls)
	}
}

// TestAgentBackground_NoSpawner — env without a spawner soft-errors,
// not a panic.
func TestAgentBackground_NoSpawner(t *testing.T) {
	out, _ := AgentBackgroundTool{}.Call(context.Background(), map[string]any{
		"prompt": "x",
	}, &engine.ToolEnv{Spawner: nil})
	if !out.IsError {
		t.Errorf("missing spawner should soft-error: %+v", out)
	}
}

// TestAgentBackground_SyncOnlySpawner — when env.Spawner is wired but
// doesn't implement AsyncSpawner (legacy sync-only engines), the tool
// soft-errors with a guidance message.
func TestAgentBackground_SyncOnlySpawner(t *testing.T) {
	env := &engine.ToolEnv{Spawner: syncOnlySpawner{}}
	out, _ := AgentBackgroundTool{}.Call(context.Background(), map[string]any{
		"prompt": "x",
	}, env)
	if !out.IsError {
		t.Errorf("sync-only spawner should soft-error")
	}
	if !strings.Contains(flatten(out), "use Agent for synchronous") {
		t.Errorf("expected guidance to fall back to Agent: %s", flatten(out))
	}
}

// TestAgentBackground_MissingPrompt — empty prompt is a soft error.
func TestAgentBackground_MissingPrompt(t *testing.T) {
	env := &engine.ToolEnv{Spawner: &stubAsyncSpawner{}}
	out, _ := AgentBackgroundTool{}.Call(context.Background(), map[string]any{}, env)
	if !out.IsError || !strings.Contains(flatten(out), "prompt is required") {
		t.Errorf("missing prompt should soft-error with helpful text: %+v", out)
	}
}

// TestAgentBackground_SpawnerError — when SpawnAsync itself returns
// an error (e.g. ErrAsyncUnavailable from a misconfigured engine),
// the tool surfaces it to the model as a soft error.
func TestAgentBackground_SpawnerError(t *testing.T) {
	spawner := &stubAsyncSpawner{spawnErr: errors.New("not enabled")}
	env := &engine.ToolEnv{Spawner: spawner}
	out, _ := AgentBackgroundTool{}.Call(context.Background(), map[string]any{
		"prompt": "x",
	}, env)
	if !out.IsError {
		t.Errorf("spawn failure should soft-error")
	}
	if !strings.Contains(flatten(out), "not enabled") {
		t.Errorf("spawn error message should propagate: %s", flatten(out))
	}
}

// syncOnlySpawner intentionally implements only AgentSpawner (the
// sync interface), not AsyncSpawner — exercises the type assertion
// fallback in AgentBackgroundTool.Call.
type syncOnlySpawner struct{}

func (syncOnlySpawner) Spawn(_ context.Context, _ engine.AgentSpawnRequest) (*engine.AgentSpawnResult, error) {
	return &engine.AgentSpawnResult{Output: "sync"}, nil
}
