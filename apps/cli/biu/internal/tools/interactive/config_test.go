package interactive

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// stagedConfig writes a fixture settings tree under tmp + sets HOME
// so the tool reads from there.
func stagedConfig(t *testing.T, userBody string) (env *engine.ToolEnv, home string, project string) {
	t.Helper()
	home = t.TempDir()
	project = t.TempDir()
	t.Setenv("HOME", home)
	if userBody != "" {
		if err := os.MkdirAll(filepath.Join(home, ".biumind"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".biumind", "settings.json"),
			[]byte(userBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &engine.ToolEnv{Cwd: project}, home, project
}

// TestConfig_Get_FromUser — read a key that lives in the user layer.
func TestConfig_Get_FromUser(t *testing.T) {
	env, _, _ := stagedConfig(t,
		`{"default": {"model": "claude-opus-4-7"}, "permissions": {"mode": "default"}}`)
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "get", "key": "default.model",
	}, env)
	body := flatten(res)
	if !strings.Contains(body, "claude-opus-4-7") {
		t.Errorf("expected model value: %s", body)
	}
}

// TestConfig_Get_Unset — unknown key reports "unset" instead of erroring.
func TestConfig_Get_Unset(t *testing.T) {
	env, _, _ := stagedConfig(t, `{}`)
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "get", "key": "default.model",
	}, env)
	if !strings.Contains(flatten(res), "unset") {
		t.Errorf("unset key should report unset: %s", flatten(res))
	}
}

// TestConfig_Get_ProjectLayerWins — project layer's value beats the
// user layer's same key (precedence: local > project > user).
func TestConfig_Get_ProjectLayerWins(t *testing.T) {
	env, _, project := stagedConfig(t,
		`{"default": {"model": "user-model"}}`)
	if err := os.MkdirAll(filepath.Join(project, ".biumind"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".biumind", "settings.json"),
		[]byte(`{"default": {"model": "project-model"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "get", "key": "default.model",
	}, env)
	if !strings.Contains(flatten(res), "project-model") {
		t.Errorf("project layer should override user: %s", flatten(res))
	}
}

// TestConfig_Set_WritesUserLayer — set creates ~/.biumind/settings.json
// when it doesn't exist; subsequent get reads the new value.
func TestConfig_Set_WritesUserLayer(t *testing.T) {
	env, home, _ := stagedConfig(t, "")
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "set", "key": "default.model", "value": "claude-opus-4-7",
	}, env)
	if res.IsError {
		t.Fatalf("set should succeed: %s", flatten(res))
	}
	// Verify file on disk.
	path := filepath.Join(home, ".biumind", "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse written file: %v", err)
	}
	def, _ := parsed["default"].(map[string]any)
	if def["model"] != "claude-opus-4-7" {
		t.Errorf("written value mismatch: %+v", parsed)
	}
}

// TestConfig_Set_PreservesOtherKeys — set on one key leaves siblings
// alone (atomic merge, not overwrite-the-file).
func TestConfig_Set_PreservesOtherKeys(t *testing.T) {
	env, home, _ := stagedConfig(t,
		`{"default": {"model": "old", "provider": "anthropic"}, "other": "kept"}`)
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "set", "key": "default.model", "value": "new",
	}, env)
	if res.IsError {
		t.Fatalf("set: %s", flatten(res))
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".biumind", "settings.json"))
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	if def, _ := parsed["default"].(map[string]any); def["provider"] != "anthropic" {
		t.Errorf("provider sibling lost: %+v", parsed)
	}
	if parsed["other"] != "kept" {
		t.Errorf("top-level sibling lost: %+v", parsed)
	}
}

// TestConfig_Set_RequiresKeyAndValue — both are required.
func TestConfig_Set_RequiresKeyAndValue(t *testing.T) {
	env, _, _ := stagedConfig(t, "")
	r1, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "set",
	}, env)
	if !r1.IsError || !strings.Contains(flatten(r1), "key is required") {
		t.Errorf("missing key should soft-error")
	}
	r2, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "set", "key": "x.y",
	}, env)
	if !r2.IsError || !strings.Contains(flatten(r2), "value is required") {
		t.Errorf("missing value should soft-error")
	}
}

// TestConfig_List_RendersResolved — list dumps the merged tree.
func TestConfig_List_RendersResolved(t *testing.T) {
	env, _, _ := stagedConfig(t, `{"a": 1, "b": {"c": "x"}}`)
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "list",
	}, env)
	body := flatten(res)
	if !strings.Contains(body, "\"a\"") || !strings.Contains(body, "\"c\"") {
		t.Errorf("list should render keys: %s", body)
	}
}

// TestConfig_List_Empty — list with no settings reports the empty
// state nicely.
func TestConfig_List_Empty(t *testing.T) {
	env, _, _ := stagedConfig(t, "")
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "list",
	}, env)
	if !strings.Contains(flatten(res), "Settings are empty") {
		t.Errorf("empty list should be friendly: %s", flatten(res))
	}
}

// TestConfig_UnknownAction — typo is a soft error.
func TestConfig_UnknownAction(t *testing.T) {
	env, _, _ := stagedConfig(t, "")
	res, _ := ConfigTool{}.Call(context.Background(), map[string]any{
		"action": "delete",
	}, env)
	if !res.IsError {
		t.Errorf("unknown action should soft-error")
	}
}

// TestConfig_DeclarativeFlags — get / list are read-only; set is not.
func TestConfig_DeclarativeFlags(t *testing.T) {
	tool := ConfigTool{}
	if !tool.IsReadOnly(map[string]any{"action": "get"}) {
		t.Errorf("get should be read-only")
	}
	if !tool.IsReadOnly(map[string]any{"action": "list"}) {
		t.Errorf("list should be read-only")
	}
	if tool.IsReadOnly(map[string]any{"action": "set"}) {
		t.Errorf("set should NOT be read-only")
	}
}
