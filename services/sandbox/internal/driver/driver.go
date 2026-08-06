// Package driver — pluggable sandbox backends.
//
// The sandbox service exposes a stable HTTP surface; drivers swap behind it
// based on what the host can offer:
//
//	stub    — in-memory, zero isolation; for unit tests + docs
//	docker  — `docker run --network=none` containers with cpu/mem limits
//	(later) k8s — Pod with gVisor runtimeClass + ResourceQuota + NetworkPolicy
//
// The interface is deliberately narrow: anything the Runtime needs (create,
// stream-exec, lifecycle, snapshot) and nothing more. We do NOT expose
// container-runtime concepts (image pulls, port maps, mounts) at this layer
// because they're driver-specific and would leak abstraction.
package driver

import (
	"context"
	"errors"
	"io"
	"time"
)

// Sandbox describes a live sandbox owned by the service.
type Sandbox struct {
	ID         string
	OwnerID    string // user id from JWT
	Image      string // driver-specific identifier (e.g. "python:3.12-slim")
	Status     string // creating | running | paused | exited | destroyed
	CreatedAt  time.Time
	CPUShares  int // 1024 = 1 core (best-effort)
	MemoryMB   int
	NetworkOff bool // true → driver enforces no egress (default)
}

// CreateInput is what the API hands the driver to spin up a sandbox.
type CreateInput struct {
	OwnerID    string
	Image      string
	CPUShares  int
	MemoryMB   int
	NetworkOff bool
	// Egress allowlist of host[:port] entries; ignored when NetworkOff is
	// true. The driver decides how to enforce: Docker uses iptables on a
	// custom bridge; K8s uses NetworkPolicy egress rules.
	EgressAllow []string
	// Env vars copied into the sandbox at start time.
	Env map[string]string
	// Optional human label so dashboards / logs can group sandboxes by use.
	Label string
}

// ExecInput streams a command into a running sandbox.
type ExecInput struct {
	SandboxID string
	Argv      []string
	// Working directory inside the sandbox. Empty = driver default.
	Workdir string
	// Optional stdin payload. Closed before the command starts so commands
	// that read from stdin (heredocs, `cat`) terminate naturally.
	Stdin []byte
	// Hard-stop after this many seconds. 0 = no timeout (driver enforces
	// its own ceiling).
	TimeoutSec int
}

// ExecResult collects what came out of a finished exec.
type ExecResult struct {
	ExitCode int
	// Set when the driver had to kill the process. Doesn't necessarily
	// indicate a hard failure — `bash -c 'exit 137'` will land here too.
	TimedOut bool
}

// Driver is what the API talks to. Methods MUST be safe for concurrent
// use — multiple agent tool calls can land at the same instant.
type Driver interface {
	Create(ctx context.Context, in CreateInput) (*Sandbox, error)
	Get(ctx context.Context, id string) (*Sandbox, error)
	List(ctx context.Context, ownerID string) ([]Sandbox, error)
	Destroy(ctx context.Context, id string) error

	// Exec writes the command to `out` (stdout interleaved with stderr,
	// prefixed line-by-line if the driver supports it) and returns the
	// final exit info. The caller is responsible for flushing `out`.
	Exec(ctx context.Context, in ExecInput, out io.Writer) (*ExecResult, error)

	// Pause/Resume map to whatever the driver offers. Drivers without a
	// real freeze (stub, future shell-only) return ErrNotSupported.
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error

	// Snapshot persists the sandbox filesystem to a driver-internal handle
	// the caller can reference later. Returns ErrNotSupported if the
	// backend has no native equivalent (in which case the caller falls
	// back to its own checkpoint / re-create strategy).
	Snapshot(ctx context.Context, id string) (snapshotID string, err error)
}

var (
	ErrNotFound     = errors.New("sandbox: not found")
	ErrNotSupported = errors.New("sandbox: not supported by driver")
	ErrInvalid      = errors.New("sandbox: invalid argument")
)
