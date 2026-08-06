//go:build linux

package preventsleep

import (
	"os/exec"
	"sync"
)

// Linux uses systemd-inhibit. Unlike caffeinate it doesn't have a
// builtin timeout, so we keep the process alive for as long as the
// user holds the lock and rely on systemd to drop it when biu exits.
type linuxImpl struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

func newPlatformImpl() platformImpl { return &linuxImpl{} }

func (l *linuxImpl) start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		return
	}
	l.running = true
	l.spawnLocked()
}

func (l *linuxImpl) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running {
		return
	}
	l.running = false
	l.killLocked()
}

func (l *linuxImpl) spawnLocked() {
	if l.cmd != nil {
		return
	}
	// --what=idle blocks idle-sleep without inhibiting the lid switch.
	// We spawn `cat` as the held process — systemd-inhibit holds the
	// lock for the lifetime of its child.
	cmd := exec.Command("systemd-inhibit",
		"--what=idle",
		"--who=biu",
		"--why=biu is processing a request",
		"--mode=block",
		"cat")
	if err := cmd.Start(); err != nil {
		return
	}
	l.cmd = cmd
	go func(c *exec.Cmd) { _ = c.Wait() }(cmd)
}

func (l *linuxImpl) killLocked() {
	if l.cmd == nil {
		return
	}
	if l.cmd.Process != nil {
		_ = l.cmd.Process.Kill()
	}
	l.cmd = nil
}
