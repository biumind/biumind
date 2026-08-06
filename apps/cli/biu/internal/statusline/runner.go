// Package statusline runs a user-configured shell command and
// returns the first non-empty line of stdout to render alongside
// biu's built-in status segments.
//
// Wire-compatible with the settings.statusLine shape:
//
//   * Type must be "command"; anything else is silently no-op'd.
//   * The command runs via `/bin/sh -c`, receives a JSON `Input`
//     describing the session on stdin, and is killed after
//     TimeoutMs (default 5 s).
//   * Only stdout from a clean exit (status 0) becomes the status
//     segment; non-zero exits / timeouts return "" so the bar
//     gracefully falls back to the built-in cluster.
//
// What we add on top:
//
//   * **Throttled cache.** The REPL re-renders on every keystroke;
//     spawning a process per render would be cruel. Runner caches
//     the last successful output for `RefreshInterval` and serves
//     it instantly to subsequent calls. Background refresh kicks
//     when the cache is older than the interval AND a render is
//     happening — no goroutines burning CPU when biu is idle.
//
//   * **Single-flight.** Two concurrent Render calls share the same
//     in-flight script process via the package's mutex; we never
//     fork twice for the same refresh window.

package statusline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout is the 5-second cap. Status lines should
// resolve quickly; users with slow scripts (a network probe, a kube
// context fetch) can override via TimeoutMs.
const DefaultTimeout = 5 * time.Second

// DefaultRefreshInterval is how stale the cached value can get
// before the next Render call triggers a fresh execution. 2 s is a
// sweet spot: short enough that the user sees git-branch changes
// promptly, long enough that key-by-key typing doesn't spam fork().
const DefaultRefreshInterval = 2 * time.Second

// Input is the JSON payload streamed to the user script's stdin.
// Field names are kept wire-compatible so users porting their
// settings.json get the same fields.
type Input struct {
	Model       string  `json:"model"`
	Cwd         string  `json:"cwd"`
	Mode        string  `json:"mode"`
	Turns       int     `json:"turns"`
	CostUSD     float64 `json:"cost_usd"`
	InputTokens int     `json:"input_tokens"`
}

// Config is the static side: the command + per-config tuning. Built
// from settings.StatusLineCommand at REPL construction time.
type Config struct {
	Command string
	Timeout time.Duration
	// Refresh is how long the cache is considered fresh. Zero =
	// DefaultRefreshInterval.
	Refresh time.Duration
}

// Enabled reports whether the config has a usable command. A nil
// Runner / empty Command yields no segment.
func (c Config) Enabled() bool {
	return c.Command != ""
}

// Runner caches the most recent successful output and rate-limits
// fresh executions. Construct via New; pass to repl model so
// statusBar can call Render(ctx, input).
type Runner struct {
	cfg Config

	mu       sync.Mutex
	lastOut  string
	lastTime time.Time
	// runFlight prevents concurrent forks. When set, a refresh is
	// already in flight — callers reuse the cached value.
	runFlight bool
}

// New returns a Runner. nil-safe everywhere: a nil pointer's Render
// returns "" immediately so callers don't need to guard.
func New(cfg Config) *Runner {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Refresh <= 0 {
		cfg.Refresh = DefaultRefreshInterval
	}
	return &Runner{cfg: cfg}
}

// Render returns the most-recent successful status-line output,
// kicking off a refresh in the foreground when the cache is older
// than Config.Refresh. Returns "" when:
//
//   - The runner is nil or has no command.
//   - The first execution hasn't completed yet (renders before any
//     successful run get nothing rather than a stale placeholder).
//   - The script exits non-zero or times out (silent degrade).
//
// The function is safe to call from any goroutine; the underlying
// cache + single-flight state are mutex-guarded.
//
// **Foreground execution caveat**: Render blocks up to Config.Timeout
// when the cache is empty (cold path on session start) so the very
// first frame after launch has a chance to display the user's
// segment. Subsequent renders hit the cache and are O(1).
func (r *Runner) Render(ctx context.Context, in Input) string {
	if r == nil || !r.cfg.Enabled() {
		return ""
	}
	r.mu.Lock()
	cached := r.lastOut
	stale := time.Since(r.lastTime) > r.cfg.Refresh
	if !stale && cached != "" {
		r.mu.Unlock()
		return cached
	}
	if r.runFlight {
		// Another goroutine is already refreshing; serve the cached
		// value (possibly "" on cold start) without piling on.
		r.mu.Unlock()
		return cached
	}
	r.runFlight = true
	r.mu.Unlock()

	out := r.runOnce(ctx, in)

	r.mu.Lock()
	r.runFlight = false
	if out != "" {
		// Only commit successful output; leave the previous good
		// value intact when the script blips so transient failures
		// don't blank the bar.
		r.lastOut = out
		r.lastTime = time.Now()
	} else if cached == "" {
		// Cold start that returned empty — record the timestamp so
		// we don't hammer fork() on every render.
		r.lastTime = time.Now()
	}
	final := r.lastOut
	r.mu.Unlock()
	return final
}

// runOnce forks the script, pipes the JSON input, and returns the
// trimmed first line of stdout on a clean exit. Errors (fork failure,
// non-zero exit, timeout, empty stdout) all collapse to "" so the
// caller can treat the result as a simple presence check.
func (r *Runner) runOnce(ctx context.Context, in Input) string {
	jsonBytes, err := json.Marshal(in)
	if err != nil {
		return ""
	}

	runCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	// /bin/sh -c keeps biu portable across darwin/linux without
	// requiring a shebang in the user's command. No env scrubbing —
	// the user owns this command.
	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", r.cfg.Command)
	cmd.Stdin = bytes.NewReader(jsonBytes)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// WaitDelay caps how long Run() waits for stdout/stderr copy
	// goroutines after the context fires SIGKILL. Without this,
	// a status-line script that fork-execs (`sleep & echo done`)
	// would hold the stdout pipe open after the parent shell
	// died — Run() then blocks for the full sleep duration even
	// though our timeout fired correctly. Linux behaves this
	// way reliably; macOS happens to close pipes more eagerly.
	// 200ms gives well-behaved scripts plenty of time to flush.
	cmd.WaitDelay = 200 * time.Millisecond

	if err := cmd.Run(); err != nil {
		// Timeout, non-zero exit, fork failure — all silent.
		// stderr is intentionally dropped; if the user wants to
		// debug they can wrap their command with `2>>~/biu-status.log`.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ""
		}
		return ""
	}

	// Trim trailing whitespace then take just the first non-empty
	// line. Multi-line scripts can output a header + detail; status
	// bar only has space for one segment.
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			return l
		}
	}
	return ""
}
