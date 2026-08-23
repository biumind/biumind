// Non-Unix stub: repo-app process management is macOS/Linux only in M1
// (TechPlan §3.3). Commands check Supported() first; this exists so the
// package still compiles cross-platform.

//go:build !darwin && !linux

package repoapp

import (
	"fmt"
	"os"
	"runtime"
)

const supportedPlatform = false

func spawnDetached(_ string, _ *os.File, _ []string, _ ...string) (int, error) {
	return 0, fmt.Errorf("repo-app detached spawn is not supported on %s (M1 is macOS/Linux only)", runtime.GOOS)
}
