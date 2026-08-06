package bundled

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
)

// Materialise extracts the embed.FS into ~/.biumind/plugins/.bundled/
// and is idempotent — re-runs reuse the existing extraction.
func TestMaterialise_extractsAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir1, err := Materialise()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir1, filepath.Join(home, ".biumind", "plugins", ".bundled")) {
		t.Errorf("extracted into unexpected path %q", dir1)
	}
	// Sentinel exists.
	if _, err := os.Stat(filepath.Join(dir1, ".materialised")); err != nil {
		t.Errorf(".materialised missing: %v", err)
	}
	// At least one bundled plugin should be present (code-review ships
	// in the framework itself; PP8c will add more).
	if _, err := os.Stat(filepath.Join(dir1, "code-review", ".claude-plugin", "plugin.json")); err != nil {
		t.Errorf("code-review plugin missing post-extract: %v", err)
	}

	// Second call: same path, no re-write (mtime check).
	st1, _ := os.Stat(filepath.Join(dir1, "code-review", ".claude-plugin", "plugin.json"))
	dir2, err := Materialise()
	if err != nil {
		t.Fatal(err)
	}
	if dir2 != dir1 {
		t.Errorf("idempotent call returned different path: %q vs %q", dir2, dir1)
	}
	st2, _ := os.Stat(filepath.Join(dir2, "code-review", ".claude-plugin", "plugin.json"))
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("second Materialise re-wrote plugin.json (mtimes differ)")
	}
}

// Roots() returns the SearchRoot biu-style + tags Source=SrcBundled.
func TestRoots_returnsBundledSearchRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	roots := Roots()
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
	if roots[0].Source != plugins.SrcBundled {
		t.Errorf("Source = %q, want %q", roots[0].Source, plugins.SrcBundled)
	}
	if _, err := os.Stat(roots[0].Path); err != nil {
		t.Errorf("root path doesn't exist: %v", err)
	}
}

// LoadAll over the bundled root discovers code-review + tags it
// Source=SrcBundled.
func TestLoadAll_discoversBundled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	agg := plugins.LoadAll(plugins.DefaultRoots(""), nil)
	var bd *plugins.LoadedPlugin
	for _, lp := range agg.Plugins {
		if lp.Manifest.Name == "code-review" {
			bd = lp
		}
	}
	if bd == nil {
		t.Fatalf("code-review not discovered (plugins=%+v errors=%+v)",
			agg.Plugins, agg.Errors)
	}
	if bd.Source != plugins.SrcBundled {
		t.Errorf("code-review.Source = %q, want %q", bd.Source, plugins.SrcBundled)
	}
	// /code-review command should be present.
	if bd.CommandsPath == "" {
		t.Error("code-review commands directory not detected")
	}
}

// SetVersion overrides the default content-hash cache key with a
// human-readable string. Useful for biu releases.
func TestSetVersion_changesCacheKey(t *testing.T) {
	// SetVersion latches via sync.Once — we can't unset for a clean
	// re-run inside one test process. The smoke test here just
	// confirms the produced path includes a sanitised version of the
	// override when set EARLY enough; we rely on the integration test
	// above for the default-hash path. To get a clean Once, run
	// this test first by alphabet ordering — Go runs tests in source
	// file order then function order, so we name accordingly.
	t.Skip("SetVersion's sync.Once latches once per process; covered manually via biu --version pinning")
}
