// Subprocess manager — owns the running `go run ./...` of the App
// being developed. Restart-safe: a slow subprocess shutdown on `r`
// won't block the CLI.

package devserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// SubprocConfig describes how to launch the App's process.
type SubprocConfig struct {
	// Dir is the project directory (where main.go lives). Required.
	Dir string

	// Cmd / Args — the launch command. Default: ["go", "run", "./..."].
	Cmd  string
	Args []string

	// Env vars merged on top of os.Environ().
	Env []string

	// LogPrefix prepended to each subprocess log line. Helps when the
	// CLI multiplexes multiple subprocesses.
	LogPrefix string

	// OnLog is called per stdout/stderr line. Use it to push log
	// events onto the dev server SSE stream.
	OnLog func(line string)
}

// SubprocMgr keeps the current process and supports graceful restart.
type SubprocMgr struct {
	cfg SubprocConfig

	mu      sync.Mutex
	cmd     *exec.Cmd
	stopped bool
}

func NewSubprocMgr(cfg SubprocConfig) *SubprocMgr {
	if cfg.Cmd == "" {
		cfg.Cmd = "go"
		cfg.Args = []string{"run", "./..."}
	}
	return &SubprocMgr{cfg: cfg}
}

// Start launches the subprocess. If one is already running, returns
// nil silently — callers should call Restart instead.
func (m *SubprocMgr) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return nil
	}
	cmd := exec.CommandContext(ctx, m.cfg.Cmd, m.cfg.Args...)
	cmd.Dir = m.cfg.Dir
	cmd.Env = append(os.Environ(), m.cfg.Env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("subproc: stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("subproc: stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("subproc: start: %w", err)
	}
	m.cmd = cmd
	m.mu.Unlock()

	go m.streamLines(stdout, "[out] ")
	go m.streamLines(stderr, "[err] ")
	return nil
}

// PID returns the current subprocess pid, or 0 if not running.
func (m *SubprocMgr) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return 0
	}
	return m.cmd.Process.Pid
}

// Stop sends SIGTERM, waits up to 3s, then SIGKILL.
func (m *SubprocMgr) Stop() error {
	m.mu.Lock()
	if m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	cmd := m.cmd
	m.cmd = nil
	m.mu.Unlock()

	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return nil
	}
}

// Restart = Stop + Start.
func (m *SubprocMgr) Restart(ctx context.Context) error {
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *SubprocMgr) streamLines(r io.Reader, src string) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" && m.cfg.OnLog != nil {
			m.cfg.OnLog(m.cfg.LogPrefix + src + line)
		}
		if err != nil {
			return
		}
	}
}
