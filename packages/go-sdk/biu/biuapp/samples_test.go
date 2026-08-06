// Sample manifests under docs/samples/app_center/ MUST validate
// against the SDK validator — drift between docs and code is the
// fastest path to confused readers writing manifests that don't
// install. This test pins them.

package biuapp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
)

// repoRoot resolves the workspace root from the SDK package's path.
// packages/go-sdk/biu/biuapp → ../../../..
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "..", "..")
}

func TestSampleManifests_Validate(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{"minimal", "minimal.yaml"},
		{"view_only", "view_only.yaml"},
		{"hybrid_full", "hybrid_full.yaml"},
		{"webview", "webview.yaml"},
		{"grid", "grid.yaml"},
		{"dashboard", "dashboard.yaml"},
		{"agent_chat", "agent_chat.yaml"},
	}
	dir := filepath.Join(repoRoot(t), "docs", "samples", "app_center")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, c.file)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("sample %s missing: %v", c.file, err)
			}
			m, err := biuapp.LoadManifest(path)
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
			if err := biuapp.Validate(m); err != nil {
				t.Errorf("strict Validate: %v", err)
			}
			if err := biuapp.ValidateBundled(m); err != nil {
				t.Errorf("ValidateBundled: %v", err)
			}
			if m.Slug() == "" {
				t.Error("Slug() empty — identifier missing")
			}
			if m.Version == "" {
				t.Error("Version empty")
			}
		})
	}
}
