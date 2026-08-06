package devserver

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWatcher_FiresOnFileWrite(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifest, []byte("v: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		Paths:    []string{dir},
		Exts:     []string{".yaml"},
		Interval: 50 * time.Millisecond,
	}

	var (
		mu      sync.Mutex
		changes [][]string
	)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(ctx, func(c []string) {
			mu.Lock()
			changes = append(changes, c)
			mu.Unlock()
		})
		close(done)
	}()

	// Wait one full poll, then write again.
	time.Sleep(120 * time.Millisecond)
	// Mtime resolution on macOS HFS is 1s; bump it explicitly so we
	// don't depend on subsecond differences during fast tests.
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(manifest, future, future)

	// Wait for at least one observed change.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(changes)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(changes) == 0 {
		t.Fatal("expected at least one change event")
	}
}

func TestWatcher_NoiseFiltered(t *testing.T) {
	cases := []string{
		"/foo/.git/HEAD",
		"/x/node_modules/pkg/index.js",
		"/y/.dart_tool/build.cache",
		"/z/vendor/github.com/x/y.go",
	}
	for _, p := range cases {
		if !isNoise(p) {
			t.Errorf("expected noise: %s", p)
		}
	}
	for _, p := range []string{
		"/foo/main.go",
		"/foo/manifest.yaml",
	} {
		if isNoise(p) {
			t.Errorf("should not be noise: %s", p)
		}
	}
}

func TestWatcher_ExtFilter(t *testing.T) {
	w := &Watcher{Exts: []string{".go", ".yaml"}}
	if !w.matchExt("/foo.go") {
		t.Error(".go should match")
	}
	if w.matchExt("/foo.txt") {
		t.Error(".txt should not match")
	}
	// Empty filter matches everything.
	w2 := &Watcher{}
	if !w2.matchExt("/anything.xyz") {
		t.Error("empty filter should match all")
	}
}
