package policies

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDirOrdering(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "20-second.cedar"), []byte("// SECOND"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "10-first.cedar"), []byte("// FIRST"), 0644))
	must(t, os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("// not cedar"), 0644))

	out, files, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "10-first.cedar" || files[1] != "20-second.cedar" {
		t.Errorf("files = %v", files)
	}
	idxFirst := strings.Index(string(out), "FIRST")
	idxSecond := strings.Index(string(out), "SECOND")
	if idxFirst < 0 || idxSecond < 0 || idxFirst > idxSecond {
		t.Errorf("ordering wrong: first@%d second@%d", idxFirst, idxSecond)
	}
}

func TestLoadDirEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := LoadDir(dir); err == nil {
		t.Fatal("expected error on empty dir")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
