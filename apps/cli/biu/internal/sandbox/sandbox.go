// Package sandbox wraps a shell command with platform-appropriate
// confinement so user-typed (or LLM-generated) commands can't trash
// the host outside the project.
//
// Strategy per platform:
//
//   darwin  → sandbox-exec with a profile that allows reads + writes
//             only inside cwd, denies network unless explicitly opted-in.
//   linux   → bwrap (Bubblewrap) when present; otherwise unconfined.
//             We don't try unshare/landlock in P0.
//   windows → unconfined; PowerShell-only sandboxing is out of scope
//             for now.
//
// The wrapper is conservative: when the platform's sandbox helper is
// missing, we fall back to running the command directly so the agent
// stays useful — but emit a Mode of "off" so the caller can warn the
// user.

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Mode tells the caller which sandbox layer actually wrapped the
// command. Useful for the UI ("⏵⏵ unsandboxed bash" vs "⏵⏵ bash @ macos").
type Mode string

const (
	ModeOff        Mode = "off"           // running as a bare child process
	ModeMacOS      Mode = "macos-sandbox" // sandbox-exec on darwin
	ModeBubblewrap Mode = "bwrap"         // bwrap on linux
)

// Options control how strict the sandbox should be.
//
// The simple set (Cwd / AllowNetwork / ReadOnly) is enough for most
// callers — the defaults block writes outside cwd and DNS/outbound
// TCP. The layered set (FSRead* / FSWrite*) lets callers tighten or
// relax specific paths beyond the defaults; they're additive on top
// of the cwd-write rule. Disable is the per-call escape hatch
// equivalent to ModeOff regardless of OS, used when the user has
// explicitly OK'd an unsandboxed command.
//
// Layered fields follow a read/write deny/allow matrix so a future
// settings.json migration plugs in directly:
//
//	FSReadDeny             ↔ fsRead.denyOnly
//	FSReadAllowWithinDeny  ↔ fsRead.allowWithinDeny
//	FSWriteAllowExtra      ↔ fsWrite.allowOnly  (extra; cwd is implicit)
//	FSWriteDenyWithinAllow ↔ fsWrite.denyWithinAllow
//
// All fields holding paths must be ABSOLUTE; relative paths are
// silently dropped to avoid escape via "../". The caller is
// responsible for filepath.Abs-ing user input.
type Options struct {
	// Cwd is the directory the command runs in AND a writable root
	// by default (writes outside Cwd are blocked unless added via
	// FSWriteAllowExtra).
	Cwd string

	// AllowNetwork lets the command reach external hosts. False
	// blocks DNS + outbound TCP on macOS.
	AllowNetwork bool

	// ReadOnly forbids any writes — even inside Cwd. Used when the
	// agent flags a tool call as "read-only" but the LLM still chose
	// to spawn a shell.
	ReadOnly bool

	// FSReadDeny lists absolute paths the sandbox blocks reads to.
	// Typical entries: ~/.ssh, ~/.aws, ~/.config/gcloud — directories
	// holding credentials the model has no business reading. The
	// default profile allows reads everywhere; use this list to
	// punch in deny rules.
	FSReadDeny []string

	// FSReadAllowWithinDeny re-permits reads inside an FSReadDeny
	// entry. Useful when one file in an otherwise-blocked tree must
	// stay reachable (e.g. allow ~/.aws/config but keep credentials
	// blocked).
	FSReadAllowWithinDeny []string

	// FSWriteAllowExtra adds writable roots beyond Cwd. Common
	// addition: a sibling output directory the model legitimately
	// writes to. Cwd is implicitly always writable; listing it again
	// is a no-op.
	FSWriteAllowExtra []string

	// FSWriteDenyWithinAllow lists absolute paths that stay
	// non-writable even when an enclosing FSWriteAllowExtra root
	// permits writes. The "carve a hole" complement of
	// FSReadAllowWithinDeny.
	FSWriteDenyWithinAllow []string

	// Disable bypasses every sandbox layer. Equivalent to ModeOff on
	// every platform. Caller must have user consent — biu surfaces
	// this through BashTool's `dangerously_disable_sandbox: true`
	// input and the permission ask flow.
	Disable bool
}

// Wrap returns a *exec.Cmd that runs `command` (a single shell string,
// not argv) under the active sandbox layer for the host OS. Mode tells
// the caller which layer was actually used.
//
// The shell is always /bin/sh (or `sh` on Windows fallback). Callers
// that need a specific interpreter should invoke it inside the
// command string ("bash -c '...'", "pwsh -c '...'").
func Wrap(ctx context.Context, command string, opt Options) (*exec.Cmd, Mode) {
	// Per-call disable: equivalent to fallback path on every OS.
	// Caller must have user consent — there's no "are you sure"
	// gate here; that's the permission layer's job.
	if opt.Disable {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Dir = opt.Cwd
		return cmd, ModeOff
	}
	switch runtime.GOOS {
	case "darwin":
		if path, err := exec.LookPath("sandbox-exec"); err == nil {
			profile := buildMacProfile(opt)
			args := []string{"-p", profile, "/bin/sh", "-c", command}
			cmd := exec.CommandContext(ctx, path, args...)
			cmd.Dir = opt.Cwd
			return cmd, ModeMacOS
		}
	case "linux":
		if path, err := exec.LookPath("bwrap"); err == nil && bwrapUsable(path) {
			args := buildBwrapArgs(opt)
			args = append(args, "/bin/sh", "-c", command)
			cmd := exec.CommandContext(ctx, path, args...)
			cmd.Dir = opt.Cwd
			return cmd, ModeBubblewrap
		}
	}
	// Fallback — unconfined (no helper found). Same shape as
	// Disable, but Mode stays "off" so the caller can still see the
	// difference via opt.Disable.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = opt.Cwd
	return cmd, ModeOff
}

// buildMacProfile produces the SBPL string passed to `sandbox-exec
// -p`. The profile is intentionally loose on reads (the LLM often
// needs to inspect /tmp, /etc, system frameworks) and tight on
// writes (only $cwd + caller-listed extra paths are writable).
//
// SBPL precedence: the LAST matching rule wins. We layer:
//
//  1. (allow default)              — baseline: read everything
//  2. (deny file-read* SUBPATH)    — credential roots blocked first
//  3. (allow file-read* SUBPATH)   — re-allow specific files inside
//  4. (deny file-write*)           — block all writes
//  5. (allow file-write* SUBPATH)  — cwd + extras allowed
//  6. (deny file-write* SUBPATH)   — surgical denies inside allow
//
// So a path appearing in both FSWriteAllowExtra and
// FSWriteDenyWithinAllow lands as denied (rule 6 fires last) — the
// "carve a hole inside an allow root" semantics.
func buildMacProfile(opt Options) string {
	cwd, _ := absOrEmpty(opt.Cwd)
	if cwd == "" {
		cwd = "/tmp"
	}
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")

	// ── Read denies (deny first, then re-allow specific subpaths) ─
	for _, p := range absOnly(opt.FSReadDeny) {
		fmt.Fprintf(&b, "(deny file-read* (subpath %q))\n", p)
	}
	for _, p := range absOnly(opt.FSReadAllowWithinDeny) {
		fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", p)
	}

	// ── Writes ────────────────────────────────────────────────────
	if opt.ReadOnly {
		b.WriteString("(deny file-write*)\n")
		// Even read-only commands need /dev/null + std streams to work
		// — `cmd 2>/dev/null` is in basically every shell pipeline. Not
		// allowing them means common idioms silently break the pipe.
		b.WriteString("(allow file-write* (literal \"/dev/null\")" +
			" (literal \"/dev/stdout\") (literal \"/dev/stderr\")" +
			" (literal \"/dev/tty\") (literal \"/dev/dtracehelper\"))\n")
	} else {
		// Block writes outside cwd. /tmp + /var/folders are always
		// writable so common build tooling (mktemp, npm cache) keeps
		// working. /dev/null + std streams + tty are required for
		// any shell that uses redirection (`>/dev/null`, `2>&1`,
		// `tee /dev/tty`) — without them, sandboxed commands silently
		// break in confusing ways (the pipeline aborts when the redirect
		// target is denied, leaving the user staring at "0" instead of
		// the real ls output).
		b.WriteString("(deny file-write*)\n")
		fmt.Fprintf(&b, "(allow file-write* (subpath %q))\n", cwd)
		b.WriteString("(allow file-write* (subpath \"/tmp\"))\n")
		b.WriteString("(allow file-write* (subpath \"/private/var/folders\"))\n")
		b.WriteString("(allow file-write* (subpath \"/private/tmp\"))\n")
		b.WriteString("(allow file-write* (literal \"/dev/null\")" +
			" (literal \"/dev/stdout\") (literal \"/dev/stderr\")" +
			" (literal \"/dev/tty\") (literal \"/dev/dtracehelper\"))\n")
		// Caller-supplied extra writable roots come AFTER the
		// defaults so they don't get masked by the default deny.
		for _, p := range absOnly(opt.FSWriteAllowExtra) {
			fmt.Fprintf(&b, "(allow file-write* (subpath %q))\n", p)
		}
		// And then the surgical denies inside those allows.
		for _, p := range absOnly(opt.FSWriteDenyWithinAllow) {
			fmt.Fprintf(&b, "(deny file-write* (subpath %q))\n", p)
		}
	}
	if !opt.AllowNetwork {
		b.WriteString("(deny network*)\n")
		b.WriteString("(allow network-bind (local ip)) ; loopback only\n")
	}
	return b.String()
}

// buildBwrapArgs builds the bwrap argv prefix.
//
// Mapping Options fields to bwrap flags:
//
//	FSReadDeny             → --tmpfs <path>  (mask the dir with empty tmpfs)
//	FSReadAllowWithinDeny  → --ro-bind <path> <path>  (re-mount specific
//	                          files read-only on top of the tmpfs mask)
//	FSWriteAllowExtra      → --bind <path> <path>      (read-write mount)
//	FSWriteDenyWithinAllow → --ro-bind <path> <path>   (downgrade to RO
//	                          on top of an enclosing rw mount)
//
// bwrap evaluates mount args in order, so later mounts override
// earlier ones — that's what makes the "deny within allow" carving
// work. SBPL on macOS does the same with last-rule-wins.
func buildBwrapArgs(opt Options) []string {
	cwd, _ := absOrEmpty(opt.Cwd)
	if cwd == "" {
		cwd = "/tmp"
	}
	args := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}
	// Read denies: mask each path with an empty tmpfs so the command
	// sees an empty directory instead of the contents.
	for _, p := range absOnly(opt.FSReadDeny) {
		args = append(args, "--tmpfs", p)
	}
	// Re-allow inside the masked area. Re-mount as read-only so the
	// command can read but not modify.
	for _, p := range absOnly(opt.FSReadAllowWithinDeny) {
		args = append(args, "--ro-bind", p, p)
	}
	if !opt.ReadOnly {
		args = append(args, "--bind", cwd, cwd)
		// Extra writable roots — bind read-write on top.
		for _, p := range absOnly(opt.FSWriteAllowExtra) {
			args = append(args, "--bind", p, p)
		}
		// Surgical denies inside those: downgrade to read-only.
		for _, p := range absOnly(opt.FSWriteDenyWithinAllow) {
			args = append(args, "--ro-bind", p, p)
		}
	}
	if !opt.AllowNetwork {
		args = append(args, "--unshare-net")
	}
	args = append(args, "--chdir", cwd)
	return args
}

// bwrapUsable reports whether bwrap on this host can actually
// create unprivileged user namespaces — the kernel feature bwrap
// relies on for `--unshare-*` and the bind/ro-bind isolation. We
// probe once per process by spawning a no-op `/bin/true` under
// the same isolation flags Wrap normally uses; the result is
// cached so subsequent calls cost nothing.
//
// Why probing matters:
//   - Hardened distros (Ubuntu 24.04 in some configs, RHEL family)
//     ship with kernel.unprivileged_userns_clone=0, which makes
//     `unshare(CLONE_NEWUSER)` return EPERM. bwrap propagates the
//     error verbatim ("Creating new namespace failed: Operation
//     not permitted") and exits 1 — every subsequent biu Bash
//     call would surface that as a sandbox failure rather than
//     silently falling back to unconfined execution.
//   - Default Docker containers also disable user namespaces
//     unless the operator passes --privileged or the right
//     capability bundle. Jenkins agents typically run inside such
//     containers, and forcing operators to grant --privileged
//     just to make biu's sandbox compile-test happy is a real
//     security regression.
//
// Without the probe, biu would either:
//
//	(a) report bwrap mode while every command actually fails, or
//	(b) require operators to harden their CI containers.
//
// Falling back to ModeOff is the lesser evil — the caller still
// sees the mode label "off" and can decide whether to allow the
// command at the permission layer.
//
// Probe budget: 2s with a no-op child. Long enough that a slow
// kernel doesn't false-negative; short enough that biu startup
// isn't noticeably affected even when bwrap is broken.
var (
	bwrapProbeOnce sync.Once
	bwrapWorks     bool
)

func bwrapUsable(bwrapPath string) bool {
	bwrapProbeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Use the same namespace flags a real Wrap call would set
		// so we test the actual capability surface, not a softer
		// approximation. /bin/true is the canonical "do nothing"
		// program; if it can't even start under bwrap, no real
		// command will.
		err := exec.CommandContext(ctx, bwrapPath,
			"--ro-bind", "/", "/",
			"--dev", "/dev",
			"--proc", "/proc",
			"--unshare-user",
			"/bin/true",
		).Run()
		bwrapWorks = err == nil
	})
	return bwrapWorks
}

// absOnly filters a slice to absolute paths only — relative paths
// would be ambiguous (relative to whose cwd?) and could be exploited
// to escape the sandbox via "../". We silently drop instead of
// erroring so a partial config still produces a working sandbox.
func absOnly(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if filepathIsAbs(p) {
			out = append(out, p)
		}
	}
	return out
}

// absOrEmpty resolves p to an absolute path; returns "" when p is
// empty or the resolution fails (caller picks a sensible fallback).
func absOrEmpty(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	abs, err := absPath(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func absPath(p string) (string, error) {
	if filepathIsAbs(p) {
		return p, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return cwd + string(os.PathSeparator) + p, nil
}

// filepathIsAbs avoids a path/filepath import for this single check.
func filepathIsAbs(p string) bool {
	return strings.HasPrefix(p, "/")
}
