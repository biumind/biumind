// Process lifecycle for repo-app instances: detached spawn with output
// redirected into logs/run.log, PID tracked in runner.pid (procmgmt
// semantics), stop = SIGTERM → 3s grace → SIGKILL, and an HTTP health
// check before the URL is announced.

package repoapp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/procmgmt"
)

// maxLogBytes mirrors serve_cmd.go's daemon log policy: truncate the
// previous run log above 10MB rather than rotating (single file, no
// dependency on a rotation library).
const maxLogBytes = 10 << 20

// stopGrace is the SIGTERM → SIGKILL escalation window for repo-app
// runners (TechPlan §3.3: 3s).
const stopGrace = 3 * time.Second

// healthTimeout bounds how long Start waits for the app to answer its
// health path before declaring the run failed.
const healthTimeout = 60 * time.Second

// Runner starts/stops/inspects repo-app instances from a Store.
type Runner struct {
	Store *Store
}

// StartOptions tunes a single Start call.
type StartOptions struct {
	Port int // 0 = OS-assigned
}

// Start launches the instance detached (or reuses the live one) and
// returns the loopback URL once the health check passes. Idempotent:
// an already-running, already-healthy instance is returned as-is.
func (r *Runner) Start(ctx context.Context, slug string, opts StartOptions) (string, error) {
	if !Supported() {
		return "", fmt.Errorf("repo-app is not supported on Windows yet (M1 is macOS/Linux only)")
	}
	inst := r.Store.Instance(slug)
	ri, err := LoadRuntime(inst.Dir)
	if err != nil {
		return "", fmt.Errorf("instance %q is not installed (run `biu repo-app install` first): %w", slug, err)
	}

	if pid, running := r.Status(slug); running {
		url := fmt.Sprintf("http://127.0.0.1:%d", ri.Port)
		if err := waitHealthy(ctx, url, ri.EffectiveHealthPath(), 10*time.Second); err == nil {
			return url, nil // reuse the live instance
		}
		// PID alive but unhealthy — bounce it rather than stacking a
		// second copy on a new port.
		_ = r.Stop(slug)
		_ = pid
	}

	port := opts.Port
	if port == 0 {
		port, err = freePort()
		if err != nil {
			return "", fmt.Errorf("allocate port: %w", err)
		}
	}
	ri.Port = port
	ri.UpdatedAt = time.Now()
	if err := SaveRuntime(inst.Dir, ri); err != nil {
		return "", err
	}

	// Truncate a previous oversized log before appending (serve_cmd.go's
	// setupDaemonLog policy).
	if fi, statErr := os.Stat(inst.LogPath()); statErr == nil && fi.Size() > maxLogBytes {
		_ = os.Truncate(inst.LogPath(), 0)
	}
	logF, err := os.OpenFile(inst.LogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer logF.Close()

	// Child env: process env + instance .env + runner-injected PORT.
	// PORT wins over any .env entry — the runner owns port allocation.
	envMap, err := LoadEnvFile(inst.EnvPath())
	if err != nil {
		return "", fmt.Errorf("read .env: %w", err)
	}
	envMap["PORT"] = strconv.Itoa(port)
	env := mergeEnv(os.Environ(), envMap)
	env = envWithPathExtra(env, ri.PathExtra)

	pid, err := spawnDetached(inst.RepoDir(), logF, env, "sh", "-c", ri.StartCmd)
	if err != nil {
		return "", fmt.Errorf("spawn %q: %w", ri.StartCmd, err)
	}
	if err := os.WriteFile(inst.PIDPath(), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		procmgmt.TerminatePID(pid)
		return "", err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitHealthy(ctx, url, ri.EffectiveHealthPath(), healthTimeout); err != nil {
		tail := tailLog(inst.LogPath(), 20)
		return "", fmt.Errorf("app did not become healthy within %s: %w\nlast log lines:\n%s",
			healthTimeout, err, tail)
	}
	return url, nil
}

// Stop terminates the instance's runner (SIGTERM → 3s → SIGKILL) and
// removes the pid file. Stopping a non-running instance is a no-op.
func (r *Runner) Stop(slug string) error {
	inst := r.Store.Instance(slug)
	pid, ok := procmgmt.ReadPID(inst.PIDPath())
	if !ok {
		return nil
	}
	if procmgmt.ProcessAlive(pid) {
		procmgmt.TerminatePIDTimeout(pid, stopGrace)
	}
	procmgmt.ReleasePIDFile(inst.PIDPath())
	return nil
}

// Status reports whether the instance's runner process is alive, plus
// its pid. Liveness is pid-based only; health is Start's concern.
func (r *Runner) Status(slug string) (pid int, running bool) {
	pid, ok := procmgmt.ReadPID(r.Store.Instance(slug).PIDPath())
	if !ok || !procmgmt.ProcessAlive(pid) {
		return 0, false
	}
	return pid, true
}

// Logs writes the instance run log to w. follow=true keeps polling for
// appended content (tail -f semantics, 500ms interval) until ctx is
// cancelled.
func (r *Runner) Logs(ctx context.Context, slug string, follow bool, w io.Writer) error {
	path := r.Store.Instance(slug).LogPath()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no run log yet for %q (has it been started?)", slug)
		}
		return err
	}
	defer f.Close()
	for {
		if _, err := io.Copy(w, f); err != nil {
			return err
		}
		if !follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// freePort grabs an OS-assigned loopback port by binding :0 and
// releasing it. The brief race before the child rebinds is accepted
// practice for port-0 semantics.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// waitHealthy polls GET <base><healthPath> until it answers with a
// non-5xx status, the timeout elapses, or ctx is cancelled. A 5xx is
// retried (app still initialising); a timeout is the failure mode.
func waitHealthy(ctx context.Context, base, healthPath string, timeout time.Duration) error {
	if !strings.HasPrefix(healthPath, "/") {
		healthPath = "/" + healthPath
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+healthPath, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			code := resp.StatusCode
			resp.Body.Close()
			if code < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("GET %s%s never answered healthy", base, healthPath)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// mergeEnv overlays kv onto a `KEY=VALUE` slice; keys in kv replace any
// existing entry (runner-injected PORT beats a .env PORT).
func mergeEnv(base []string, kv map[string]string) []string {
	out := make([]string, 0, len(base)+len(kv))
	for _, entry := range base {
		k := entry[:strings.IndexByte(entry, '=')]
		if _, overridden := kv[k]; overridden {
			continue
		}
		out = append(out, entry)
	}
	for k, v := range kv {
		out = append(out, k+"="+v)
	}
	return out
}

// tailLog returns the last n lines of the log for error messages.
func tailLog(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "(no log)"
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
