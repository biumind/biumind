// Detached spawn for macOS/Linux: new session (Setsid) so closing the
// terminal / CLI doesn't take the app down, stdio redirected into the
// instance run log. Build-tagged in the oskeychain file-per-platform
// style.

//go:build darwin || linux

package repoapp

import (
	"os"
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

	proc, err := os.StartProcess(argv[0], argv, &os.ProcAttr{
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
