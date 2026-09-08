package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectDirSanitisesToSingleComponent(t *testing.T) {
	got := ProjectDir("/Users/didi/work spaces/biu-mind")
	want := "-Users-didi-work-spaces-biu-mind"
	if got != want {
		t.Errorf("ProjectDir = %q, want %q", got, want)
	}
	if strings.ContainsRune(got, os.PathSeparator) {
		t.Errorf("project dir must be a single path component: %q", got)
	}
}

func TestProjectDirResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	viaLink := ProjectDir(link)
	viaReal := ProjectDir(real)
	if viaLink != viaReal {
		t.Errorf("symlink and target must bucket together: %q vs %q", viaLink, viaReal)
	}
}

func TestProjectDirLongPathGetsHashSuffix(t *testing.T) {
	long := "/" + strings.Repeat("averydeepdirectoryname", 20) // 441 chars
	got := ProjectDir(long)
	// 200 truncated + '-' + 8 hex chars.
	if len(got) != maxProjectDirLen+1+8 {
		t.Errorf("long path should truncate to %d chars, got %d", maxProjectDirLen+1+8, len(got))
	}
	// Deterministic + distinct per path.
	if got != ProjectDir(long) {
		t.Errorf("hash suffix must be deterministic")
	}
	other := ProjectDir(long + "x")
	if got == other {
		t.Errorf("distinct deep paths collided: %q", got)
	}
	if ProjectDir("/short/path") == got {
		t.Errorf("short path should not be hashed")
	}
}

func TestProjectDirNormalisesRelativeAndEmpty(t *testing.T) {
	// Empty input resolves against the process cwd — never "" and
	// never panics.
	if got := ProjectDir(""); got == "" {
		t.Errorf("empty cwd should still produce a bucket name")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Skip(err)
	}
	if ProjectDir("") != ProjectDir(cwd) {
		t.Errorf("empty cwd should equal explicit cwd bucket")
	}
}
