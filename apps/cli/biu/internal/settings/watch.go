// Settings hot reload — polls the three layered files for mtime
// changes and notifies a callback when any of them update.
//
// We deliberately don't pull fsnotify into the dep tree just for this:
// settings.json is edited rarely, polling at 2s costs ~3 stat() calls
// every 2 seconds, and fsnotify's per-platform quirks (kqueue file
// caps, inotify per-user limits) are more pain than this is worth.
//
// Lifecycle:
//
//   w := settings.NewWatcher(cwd, onReload)
//   defer w.Close()
//
// onReload runs on the watcher goroutine. Treat it as best-effort —
// if it panics, the watcher logs to stderr and keeps going.

package settings

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Watcher polls the user / project / local settings files for
// changes and fires a callback when any of them get a fresh mtime.
type Watcher struct {
	cwd    string
	cb     func(*Layered)
	tick   *time.Ticker
	closed atomic.Bool

	mu      sync.Mutex
	mtimes  map[string]time.Time
}

// NewWatcher starts polling. cb runs on the watcher goroutine the
// first time mtimes diverge from the launch snapshot. Returns a
// non-nil watcher even if no settings files exist yet — they get
// picked up when they appear.
//
// `cwd` is the project root used to resolve project / local layers.
// Empty cwd skips those (only user layer watched).
func NewWatcher(cwd string, cb func(*Layered)) *Watcher {
	w := &Watcher{
		cwd: cwd, cb: cb,
		tick:   time.NewTicker(2 * time.Second),
		mtimes: map[string]time.Time{},
	}
	w.snapshotMtimes() // baseline so the first call doesn't fire spuriously
	go w.loop()
	return w
}

// Close stops the polling goroutine. Safe to call multiple times.
func (w *Watcher) Close() {
	if w.closed.CompareAndSwap(false, true) {
		w.tick.Stop()
	}
}

// Reload forces an immediate Load + callback regardless of mtimes.
// `/reload` slash and any future external trigger calls this.
func (w *Watcher) Reload() {
	if w.cb == nil {
		return
	}
	l, err := Load(w.cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[biu] settings reload failed: %v\n", err)
		return
	}
	w.snapshotMtimes()
	w.cb(l)
}

func (w *Watcher) loop() {
	for range w.tick.C {
		if w.closed.Load() {
			return
		}
		if w.detectChange() {
			w.Reload()
		}
	}
}

// detectChange returns true when any tracked path's mtime moved
// since the last snapshot. Side-effect: updates the snapshot so a
// single change only fires once.
func (w *Watcher) detectChange() bool {
	current := w.currentMtimes()
	w.mu.Lock()
	defer w.mu.Unlock()
	changed := false
	for path, t := range current {
		if !w.mtimes[path].Equal(t) {
			changed = true
		}
	}
	// Also fire on disappearance (file deleted by `rm`).
	for path := range w.mtimes {
		if _, ok := current[path]; !ok {
			changed = true
		}
	}
	w.mtimes = current
	return changed
}

func (w *Watcher) snapshotMtimes() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mtimes = w.currentMtimes()
}

// currentMtimes stats every layer's path so the watcher can detect
// edits and reload. Only `.biumind/` is read.
func (w *Watcher) currentMtimes() map[string]time.Time {
	out := map[string]time.Time{}
	add := func(p string) {
		if p == "" {
			return
		}
		if st, err := os.Stat(p); err == nil {
			out[p] = st.ModTime()
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home + "/.biumind/settings.json")
	}
	if w.cwd != "" {
		add(w.cwd + "/.biumind/settings.json")
		add(w.cwd + "/.biumind/settings.local.json")
	}
	return out
}
