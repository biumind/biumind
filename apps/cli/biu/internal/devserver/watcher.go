// Package devserver — file mtime watcher.
//
// We deliberately do not use fsnotify: 1) avoids a new dependency for
// the CLI which we ship as a single binary, 2) macOS fsnotify on dirs
// is flaky for atomic-rename editors anyway. A 500ms mtime poll gives
// dev-loop latency that's indistinguishable from native FS events for
// human-driven file saves.

package devserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Watcher polls the configured paths and fires onChange when any
// of them have a newer mtime than the previous tick.
//
// Patterns:
//   - exact file paths            → watched directly
//   - directory paths             → recursively walked, every file is a candidate
//   - extension filters (".go")   → applied to walked files
//
// The first call to Run is treated as t0; events fire from changes
// strictly after that.
type Watcher struct {
	Paths    []string      // file or directory paths
	Exts     []string      // when walking dirs, only files with one of these extensions
	Interval time.Duration // poll interval; default 500ms

	mu    sync.Mutex
	prev  map[string]time.Time
	dirty map[string]bool
}

// Run blocks until ctx is canceled. onChange is called with the set
// of changed paths whenever a poll tick observes any newer mtime.
// onChange runs synchronously on the polling goroutine — keep it
// fast or fan out to a worker.
func (w *Watcher) Run(ctx context.Context, onChange func(changed []string)) {
	if w.Interval <= 0 {
		w.Interval = 500 * time.Millisecond
	}
	w.mu.Lock()
	w.prev = w.snapshot()
	w.mu.Unlock()

	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := w.snapshot()
		var changed []string
		w.mu.Lock()
		for path, mt := range cur {
			if prev, ok := w.prev[path]; !ok || mt.After(prev) {
				changed = append(changed, path)
			}
		}
		// Detect deletions too — caller may want to know about them.
		for path := range w.prev {
			if _, ok := cur[path]; !ok {
				changed = append(changed, path+" (deleted)")
			}
		}
		w.prev = cur
		w.mu.Unlock()
		if len(changed) > 0 && onChange != nil {
			onChange(changed)
		}
	}
}

func (w *Watcher) snapshot() map[string]time.Time {
	out := map[string]time.Time{}
	for _, p := range w.Paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return nil
				}
				if !w.matchExt(path) {
					return nil
				}
				// Skip vendored / hidden / build cache directories — they
				// produce a torrent of noise events when go build runs.
				if isNoise(path) {
					return nil
				}
				out[path] = fi.ModTime()
				return nil
			})
		} else {
			out[p] = info.ModTime()
		}
	}
	return out
}

func (w *Watcher) matchExt(path string) bool {
	if len(w.Exts) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range w.Exts {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}

func isNoise(path string) bool {
	for _, seg := range []string{
		string(filepath.Separator) + ".git" + string(filepath.Separator),
		string(filepath.Separator) + "node_modules" + string(filepath.Separator),
		string(filepath.Separator) + ".dart_tool" + string(filepath.Separator),
		string(filepath.Separator) + "vendor" + string(filepath.Separator),
	} {
		if strings.Contains(path, seg) {
			return true
		}
	}
	return false
}
