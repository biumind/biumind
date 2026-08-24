// Detached spawn for macOS/Linux: new session (Setsid) so closing the
// terminal / CLI doesn't take the app down, stdio redirected into the
// instance run log. Build-tagged in the oskeychain file-per-platform
// style.

//go:build darwin || linux

package repoapp

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const supportedPlatform = true

// spawnDetached starts argv in dir as a detached session leader, with
// stdin from /dev/null and stdout+stderr appended to logF. Returns the
// child pid. The child is intentionally NOT reaped by us — it outlives
// the CLI invocation by design (stop goes through the pid file).
func spawnDetached(dir string, logF *os.File, env []string, argv ...string) (int, error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()

	// os.StartProcess does NOT search PATH (no execvp semantics), so a
	// bare "sh"/"npm" argv[0] fails with ENOENT. Resolve it first using
	// the caller's PATH (the child's env may carry a narrowed PATH).
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return 0, fmt.Errorf("spawn: resolve %q: %w", argv[0], err)
	}

	proc, err := os.StartProcess(bin, argv, &os.ProcAttr{
		Dir:   dir,
		Env:   env,
		Files: []*os.File{devNull, logF, logF},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return 0, err
	}
	pid := proc.Pid
	// Release detaches without waiting — the pid file is the handle.
	if err := proc.Release(); err != nil {
		return 0, err
	}
	return pid, nil
}
