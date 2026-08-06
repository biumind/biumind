// Sandbox hardening policy (Runtime v3 R5) — image allowlist, workspace
// tmpfs, non-root user, egress enforcement flag, workdir path defense.
//
// Shared by docker + k8s drivers. The service builds a Policy from env
// (cmd/sandbox/main.go) and hands it to NewDocker / NewK8s.

package driver

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Policy carries the deployment's sandbox hardening config.
type Policy struct {
	// DefaultImage is used when CreateInput.Image is empty. Always allowed.
	DefaultImage string
	// ImageAllowlist is the set of additional images callers may request.
	// Empty → only DefaultImage is permitted.
	ImageAllowlist []string
	// EgressEnforced=false (default) makes selective egress fail-closed:
	// a sandbox that requests EgressAllow gets network=none instead of an
	// unfiltered bridge (avoids false "restricted" security). Set true only
	// when the host's iptables-backed `biu-sbx-egress` bridge is configured.
	EgressEnforced bool
	// WorkspaceTmpfsMB sizes the ephemeral writable /workspace tmpfs (0 →
	// default). Needed because the rootfs is read-only.
	WorkspaceTmpfsMB int
	// RunAsUser is the "uid:gid" the container runs as. "" → don't set
	// --user (image default, usually root) — escape hatch if non-root
	// breaks an image.
	RunAsUser string
}

// DefaultPolicy returns the safe defaults used when env is unset.
func DefaultPolicy() Policy {
	return Policy{
		DefaultImage:     "alpine:3.20",
		ImageAllowlist:   nil,
		EgressEnforced:   false,
		WorkspaceTmpfsMB: 512,
		RunAsUser:        "65532:65532", // nonroot
	}
}

// workspaceMB returns the effective /workspace tmpfs size in MB.
func (p Policy) workspaceMB() int {
	if p.WorkspaceTmpfsMB <= 0 {
		return 512
	}
	return p.WorkspaceTmpfsMB
}

// ResolveImage validates a requested image against the allowlist and returns
// the effective image to run. Empty request → DefaultImage. DefaultImage is
// always allowed; otherwise the image must be in ImageAllowlist exactly.
func (p Policy) ResolveImage(requested string) (string, error) {
	img := strings.TrimSpace(requested)
	if img == "" {
		img = p.DefaultImage
	}
	if img == "" {
		return "", fmt.Errorf("%w: no image and no default configured", ErrInvalid)
	}
	if img == p.DefaultImage {
		return img, nil
	}
	for _, a := range p.ImageAllowlist {
		if strings.TrimSpace(a) == img {
			return img, nil
		}
	}
	return "", fmt.Errorf("%w: image %q not in allowlist", ErrInvalid, img)
}

// UserGID parses RunAsUser ("uid:gid" or "uid") into numeric ids for the
// k8s SecurityContext. ok=false when unset/unparseable (→ don't pin a user).
// gid defaults to uid when only "uid" is given.
func (p Policy) UserGID() (uid, gid int64, ok bool) {
	s := strings.TrimSpace(p.RunAsUser)
	if s == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(s, ":", 2)
	u, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, 0, false
	}
	g := u
	if len(parts) == 2 {
		if gg, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
			g = gg
		}
	}
	return u, g, true
}

// sandboxRoots are the only directory trees an exec workdir may live under.
var sandboxRoots = []string{"/workspace", "/tmp"}

// AssertSandboxPath validates an exec/create workdir. Empty is OK (driver
// default). Otherwise it must be an absolute path, free of ".." segments,
// and rooted under /workspace or /tmp — so a caller can't point execution at
// /etc, /root, or escape via traversal. Container isolation is the real
// boundary; this is defense-in-depth at the API edge.
func AssertSandboxPath(p string) error {
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: workdir must be absolute, got %q", ErrInvalid, p)
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("%w: workdir must not contain '..', got %q", ErrInvalid, p)
	}
	clean := path.Clean(p)
	for _, root := range sandboxRoots {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return nil
		}
	}
	return fmt.Errorf("%w: workdir %q must be under /workspace or /tmp", ErrInvalid, clean)
}
