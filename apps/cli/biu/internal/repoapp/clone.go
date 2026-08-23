// Git clone / update for repo-app instances. Uses the system `git`
// binary via exec (same as plugins.fetchGitMarketplace) — deliberately
// no go-git dependency.

package repoapp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CloneOrFetch makes destDir a shallow checkout of repoURL at ref.
// Fresh install: `git clone --depth 1 --branch <ref>`. Existing clone:
// `git fetch --depth 1 origin <ref>` + checkout, so updates stay cheap
// and never accumulate history. ref may be empty (default branch).
// Returns the checked-out HEAD sha.
func CloneOrFetch(ctx context.Context, repoURL, ref, destDir string) (string, error) {
	if _, err := os.Stat(filepath.Join(destDir, ".git")); err == nil {
		fetchRef := ref
		if fetchRef == "" {
			fetchRef = "HEAD"
		}
		if err := runGit(ctx, destDir, "fetch", "--depth", "1", "origin", fetchRef); err != nil {
			return "", err
		}
		// FETCH_HEAD works for branch, tag, and default-branch fetches
		// alike; detached checkout avoids stale local-branch bookkeeping.
		if err := runGit(ctx, destDir, "checkout", "--detach", "FETCH_HEAD"); err != nil {
			return "", err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
			return "", err
		}
		args := []string{"clone", "--depth", "1"}
		if ref != "" {
			args = append(args, "--branch", ref)
		}
		args = append(args, repoURL, destDir)
		if err := runGit(ctx, "", args...); err != nil {
			return "", err
		}
	}
	return HeadSHA(ctx, destDir)
}

// HeadSHA returns the full sha of HEAD in dir.
func HeadSHA(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit invokes git with the given args. cwd may be empty for commands
// that don't operate on a working tree (clone). Stderr is propagated
// into the error — git's own messages beat any wrapper we'd write
// (pattern from plugins.runGit).
func runGit(ctx context.Context, cwd string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
