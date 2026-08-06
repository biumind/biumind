//go:build darwin

package preventsleep

import (
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// caffeinate timeout. The process auto-exits after this so an
// orphaned child (we got SIGKILL'd) eventually relinquishes the
// power assertion on its own.
const caffeinateTimeout = 5 * time.Minute

// We restart caffeinate before its self-timeout so the assertion
// stays continuous while we're holding refCount > 0.
const caffeinateRestart = 4 * time.Minute

type darwinImpl struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stopCh  chan struct{}
	running bool
}

func newPlatformImpl() platformImpl { return &darwinImpl{} }

func (d *darwinImpl) start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		return
	}
	d.running = true
	d.stopCh = make(chan struct{})
	d.spawnLocked()
	go d.restartLoop(d.stopCh)
}

func (d *darwinImpl) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return
	}
	d.running = false
	if d.stopCh != nil {
		close(d.stopCh)
		d.stopCh = nil
	}
	d.killLocked()
}

// spawnLocked launches caffeinate. Caller must hold d.mu. Failures
// are silent — no caffeinate binary just means we don't get the
// assertion.
func (d *darwinImpl) spawnLocked() {
	if d.cmd != nil {
		return
	}
	cmd := exec.Command("caffeinate",
		"-i", "-t", strconv.Itoa(int(caffeinateTimeout.Seconds())))
	if err := cmd.Start(); err != nil {
		return
	}
	d.cmd = cmd
	// Reap so we don't leave a zombie when the child auto-exits.
	go func(c *exec.Cmd) { _ = c.Wait() }(cmd)
}

func (d *darwinImpl) killLocked() {
	if d.cmd == nil {
		return
	}
	if d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
	d.cmd = nil
}

// restartLoop refreshes caffeinate before its self-timeout fires.
func (d *darwinImpl) restartLoop(stopCh <-chan struct{}) {
	t := time.NewTicker(caffeinateRestart)
	defer t.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-t.C:
			d.mu.Lock()
			if d.running {
				d.killLocked()
				d.spawnLocked()
			}
			d.mu.Unlock()
		}
	}
}
