package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/settings"
)

// Schemas should serialise to valid JSON and self-identify as
// Draft 2020-12.
func TestSchemasSerializeAsJSON(t *testing.T) {
	for name, s := range map[string]map[string]any{
		"config":   ConfigSchema(),
		"settings": SettingsSchema(),
	} {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(b), "https://json-schema.org/draft/2020-12/schema") {
				t.Errorf("schema %q missing $schema header", name)
			}
			if !strings.Contains(string(b), "\"properties\"") {
				t.Errorf("schema %q has no properties block", name)
			}
		})
	}
}

func TestValidateConfigClean(t *testing.T) {
	cfg := config.Defaults()
	cfg.Default.Mode = "direct"
	cfg.Providers["anthropic"] = config.ProviderSection{APIKey: "sk-ant-xxx"}
	if reasons := ValidateConfig(cfg); len(reasons) != 0 {
		t.Errorf("clean config should have no warnings; got %v", reasons)
	}
}

func TestValidateConfigDirectMissingAPIKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Default.Mode = "direct"
	cfg.Providers = map[string]config.ProviderSection{} // no anthropic key
	got := ValidateConfig(cfg)
	if !containsSubstring(got, "api_key is missing") {
		t.Errorf("expected api_key warning; got %v", got)
	}
}

func TestValidateConfigCloudMissingHub(t *testing.T) {
	cfg := config.Defaults()
	cfg.Default.Mode = "cloud"
	cfg.Relay.Endpoint = ""
	cfg.Relay.VirtualKey = ""
	got := ValidateConfig(cfg)
	if !containsSubstring(got, "model-relay].endpoint is missing") {
		t.Errorf("expected model-relay endpoint warning; got %v", got)
	}
	if !containsSubstring(got, "model-relay].virtual_key is missing") {
		t.Errorf("expected virtual_key warning; got %v", got)
	}
}

func TestValidateConfigSearchDirectMissingURL(t *testing.T) {
	cfg := config.Defaults()
	// keep mode valid so we only test the search check
	cfg.Default.Mode = "direct"
	cfg.Providers["anthropic"] = config.ProviderSection{APIKey: "x"}
	cfg.Search.Mode = "direct"
	cfg.Search.SearxNGURL = ""
	got := ValidateConfig(cfg)
	if !containsSubstring(got, "searxng_url is missing") {
		t.Errorf("expected searxng_url warning; got %v", got)
	}
}

func TestValidateConfigUnknownMode(t *testing.T) {
	cfg := config.Defaults()
	cfg.Default.Mode = "lunar"
	got := ValidateConfig(cfg)
	if !containsSubstring(got, "default.mode=lunar") {
		t.Errorf("expected mode warning; got %v", got)
	}
}

func TestValidateSettingsClean(t *testing.T) {
	s := &settings.Settings{
		Permissions: &settings.PermissionsBlock{
			DefaultMode: "default",
			Allow:       []string{"Bash(npm test)", "Edit(./src/**)"},
			Deny:        []string{"Bash(rm -rf /)"},
		},
	}
	if reasons := ValidateSettings(s); len(reasons) != 0 {
		t.Errorf("clean settings should have no warnings; got %v", reasons)
	}
}

func TestValidateSettingsBadMode(t *testing.T) {
	s := &settings.Settings{
		Permissions: &settings.PermissionsBlock{DefaultMode: "loose"},
	}
	got := ValidateSettings(s)
	if !containsSubstring(got, "permissions.defaultMode=loose") {
		t.Errorf("expected defaultMode warning; got %v", got)
	}
}

func TestValidateSettingsMalformedRule(t *testing.T) {
	s := &settings.Settings{
		Permissions: &settings.PermissionsBlock{
			Allow: []string{"Bash(unbalanced", "*", "(no-tool-name)"},
		},
	}
	got := ValidateSettings(s)
	// Two malformed rules: index 0 (unbalanced) and index 2 (no tool prefix).
	// Index 1 ("*") is fine.
	if !containsSubstring(got, "permissions.allow[0]") {
		t.Errorf("expected warning for unbalanced rule; got %v", got)
	}
	if !containsSubstring(got, "permissions.allow[2]") {
		t.Errorf("expected warning for parens-only rule; got %v", got)
	}
	if containsSubstring(got, "permissions.allow[1]") {
		t.Errorf("rule '*' should be accepted; got %v", got)
	}
}

func TestValidateSettingsNilSafe(t *testing.T) {
	if got := ValidateSettings(nil); got != nil {
		t.Errorf("nil settings should produce nil result; got %v", got)
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
