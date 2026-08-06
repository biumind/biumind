// The settings.schema.json under docs/biu/schemas/ is the canonical
// snapshot regenerated via `biu config schema settings`. The
// official site (your-biumind.example.com) serves a copy out of
// web/site/public/schemas/biu/ so editor `$schema` resolves over
// HTTPS without requiring a public GitHub mirror.
//
// Both files MUST stay byte-identical — drift means editors get a
// stale schema while the embedded init template advertises the new
// one. This test fails loudly when the two diverge so the next
// schema update can't accidentally skip the site copy.

package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot walks up from the test file's location until it finds
// the workspace root (the directory holding both `apps/` and
// `web/site/`). Avoids hardcoded absolute paths so the test
// survives developers with different checkouts.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Dir(here)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "apps")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "web")); err == nil {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate repo root from %s", here)
	return ""
}

func TestSiteSchemaCopyMatchesDocsCopy(t *testing.T) {
	root := repoRoot(t)
	docsPath := filepath.Join(root, "docs/biu/schemas/settings.schema.json")
	sitePath := filepath.Join(root, "web/site/public/schemas/biu/settings.schema.json")

	docsBytes, err := os.ReadFile(docsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// docs/ lives in the internal monorepo and is not part of
			// every checkout (e.g. the public mirror) — nothing to
			// compare against there.
			t.Skipf("docs schema not present in this checkout: %v", err)
		}
		t.Fatalf("read docs schema: %v", err)
	}
	siteBytes, err := os.ReadFile(sitePath)
	if err != nil {
		t.Fatalf("read site schema: %v\n\nIf you regenerated the schema "+
			"via `biu config schema settings > docs/biu/schemas/...`, "+
			"also copy the result to web/site/public/schemas/biu/ — "+
			"editors resolve $schema from the latter.", err)
	}
	if !bytes.Equal(docsBytes, siteBytes) {
		t.Errorf("schema copies diverge:\n  docs: %s (%d bytes)\n  site: %s (%d bytes)\n\n"+
			"Run `cp %s %s` after regenerating to keep them in sync.",
			docsPath, len(docsBytes), sitePath, len(siteBytes), docsPath, sitePath)
	}
}
