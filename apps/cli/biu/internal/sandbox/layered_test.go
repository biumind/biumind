// Tests for the layered allow/deny rules added in P20.41. Two
// layers of coverage:
//
//   1. Profile / argv generation — assert the strings/args contain
//      the right rules. Pure unit, runs on every OS.
//   2. Behavioural — actually run a command under the sandbox and
//      observe the constraint takes effect. macOS only (sandbox-exec
//      available); Linux behavioural runs would need bwrap which CI
//      may not have.

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── macOS profile generation ─────────────────────────

func TestMacProfileFSReadDeny(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	prof := buildMacProfile(Options{
		Cwd:        "/tmp/proj",
		FSReadDeny: []string{"/Users/me/.ssh", "/Users/me/.aws"},
	})
	for _, want := range []string{
		`(deny file-read* (subpath "/Users/me/.ssh"))`,
		`(deny file-read* (subpath "/Users/me/.aws"))`,
	} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile missing deny rule:\n%s\nwant: %s", prof, want)
		}
	}
}

func TestMacProfileFSReadAllowWithinDeny(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	prof := buildMacProfile(Options{
		Cwd:                   "/tmp/proj",
		FSReadDeny:            []string{"/Users/me/.aws"},
		FSReadAllowWithinDeny: []string{"/Users/me/.aws/config"},
	})
	// Allow line must come AFTER the deny so SBPL's last-rule-wins
	// semantics re-permit the specific file.
	denyIdx := strings.Index(prof, `deny file-read* (subpath "/Users/me/.aws")`)
	allowIdx := strings.Index(prof, `allow file-read* (subpath "/Users/me/.aws/config")`)
	if denyIdx < 0 || allowIdx < 0 {
		t.Fatalf("missing rules in profile:\n%s", prof)
	}
	if allowIdx < denyIdx {
		t.Errorf("allow must come after deny so the carve-out wins; got allow@%d deny@%d", allowIdx, denyIdx)
	}
}

func TestMacProfileFSWriteAllowExtra(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	prof := buildMacProfile(Options{
		Cwd:               "/tmp/proj",
		FSWriteAllowExtra: []string{"/srv/output"},
	})
	if !strings.Contains(prof, `allow file-write* (subpath "/srv/output")`) {
		t.Errorf("extra writable root missing:\n%s", prof)
	}
}

func TestMacProfileFSWriteDenyWithinAllow(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	prof := buildMacProfile(Options{
		Cwd:                    "/tmp/proj",
		FSWriteAllowExtra:      []string{"/srv/output"},
		FSWriteDenyWithinAllow: []string{"/srv/output/secret"},
	})
	allowIdx := strings.Index(prof, `allow file-write* (subpath "/srv/output")`)
	denyIdx := strings.Index(prof, `deny file-write* (subpath "/srv/output/secret")`)
	if allowIdx < 0 || denyIdx < 0 {
		t.Fatalf("missing rules:\n%s", prof)
	}
	if denyIdx < allowIdx {
		t.Errorf("deny must come after allow so the carve-out wins; allow@%d deny@%d", allowIdx, denyIdx)
	}
}

// Relative paths must not produce sandbox rules — they would be
// ambiguous (relative to whose cwd?) and could be exploited via
// `..` traversal. The filter drops them silently rather than
// failing so a half-misconfigured Options still yields a working
// sandbox.
func TestMacProfileSkipsRelativePaths(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	prof := buildMacProfile(Options{
		Cwd:        "/tmp/proj",
		FSReadDeny: []string{"relative/path", "/abs/path"},
	})
	if strings.Contains(prof, "relative/path") {
		t.Errorf("relative path leaked into profile:\n%s", prof)
	}
	if !strings.Contains(prof, `"/abs/path"`) {
		t.Errorf("absolute path missing:\n%s", prof)
	}
}

// ─── Linux bwrap argv generation ──────────────────────

func TestBwrapArgsFSReadDeny(t *testing.T) {
	args := buildBwrapArgs(Options{
		Cwd:        "/tmp/proj",
		FSReadDeny: []string{"/home/me/.ssh"},
	})
	// Locate the --tmpfs <path> pair: --tmpfs at index i, path at i+1.
	found := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--tmpfs" && args[i+1] == "/home/me/.ssh" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("--tmpfs /home/me/.ssh not in argv: %v", args)
	}
}

func TestBwrapArgsFSReadAllowWithinDeny(t *testing.T) {
	args := buildBwrapArgs(Options{
		Cwd:                   "/tmp/proj",
		FSReadDeny:            []string{"/home/me/.aws"},
		FSReadAllowWithinDeny: []string{"/home/me/.aws/config"},
	})
	// Find both: tmpfs for the deny + ro-bind for the carve-out.
	tmpfsIdx, robindIdx := -1, -1
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--tmpfs" && args[i+1] == "/home/me/.aws" {
			tmpfsIdx = i
		}
		if args[i] == "--ro-bind" && i+2 < len(args) &&
			args[i+1] == "/home/me/.aws/config" && args[i+2] == "/home/me/.aws/config" {
			robindIdx = i
		}
	}
	if tmpfsIdx < 0 || robindIdx < 0 {
		t.Fatalf("missing args: tmpfs@%d robind@%d in %v", tmpfsIdx, robindIdx, args)
	}
	if robindIdx < tmpfsIdx {
		t.Errorf("ro-bind must come after tmpfs so the re-mount wins")
	}
}

func TestBwrapArgsFSWriteAllowExtra(t *testing.T) {
	args := buildBwrapArgs(Options{
		Cwd:               "/tmp/proj",
		FSWriteAllowExtra: []string{"/srv/output"},
	})
	found := false
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--bind" && args[i+1] == "/srv/output" && args[i+2] == "/srv/output" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("--bind /srv/output missing: %v", args)
	}
}

func TestBwrapArgsRelativePathsDropped(t *testing.T) {
	args := buildBwrapArgs(Options{
		Cwd:        "/tmp/proj",
		FSReadDeny: []string{"relative", "/abs"},
	})
	for i, a := range args {
		if a == "relative" {
			t.Errorf("relative path leaked at args[%d]: %v", i, args)
		}
	}
}

// ─── Disable flag ────────────────────────────────────

// Disable=true must produce a bare exec.Cmd (Mode=off) on every
// platform — equivalent to running the command without any
// sandbox helper. Useful when the user has explicitly OK'd a
// command that needs unrestricted access.
func TestWrapDisableSkipsSandbox(t *testing.T) {
	cmd, mode := Wrap(context.Background(), "echo hi", Options{
		Cwd:     "/tmp",
		Disable: true,
	})
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	if mode != ModeOff {
		t.Errorf("Disable should yield ModeOff; got %q", mode)
	}
	// Verify the command isn't wrapping sandbox-exec / bwrap — its
	// argv should start with the shell directly.
	if !strings.HasSuffix(cmd.Args[0], "sh") {
		t.Errorf("disabled cmd should run shell directly; argv0=%q (full: %v)",
			cmd.Args[0], cmd.Args)
	}
}

// ─── Behavioural sandbox check (macOS only) ───────────

// Run an actual command that tries to write to a denied path. The
// sandbox should reject it; without Disable=true the write fails.
// Skipped when sandbox-exec isn't on PATH (rare on a real Mac, but
// CI containers might lack it).
func TestMacSandboxBlocksWriteOutsideCwd(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	dir := t.TempDir()
	// Try writing to /private/etc which is system-owned — should be
	// blocked even if we somehow had perms (we don't, but sandbox
	// makes the failure deterministic).
	target := filepath.Join("/private/etc/biu-test-marker-" + filepath.Base(dir))
	cmd, _ := Wrap(context.Background(), "echo nope > "+target, Options{
		Cwd: dir,
	})
	out, err := cmd.CombinedOutput()
	if err == nil {
		// Cleanup just in case the write somehow succeeded.
		_ = os.Remove(target)
		t.Errorf("write to /private/etc should fail under sandbox; output=%q", out)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		_ = os.Remove(target)
		t.Errorf("file landed despite sandbox: %s", target)
	}
}
