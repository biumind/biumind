// Stub driver — in-memory, zero isolation.
//
// Two reasons to keep it around:
//   1. Unit tests for the HTTP API don't need Docker on the runner.
//   2. Smoke / dev workflows where Docker isn't available (CI ephemeral
//      runners, restricted hosts) can still exercise the wire format end
//      to end. Production must NEVER fall back to this driver — main.go
//      requires an explicit `SANDBOX_DRIVER=stub` env var to engage it.
//
// The exec handler runs the command **on the host process**. This is by
// design: agent tool calls in stub mode are functionally equivalent to
// Runtime's local-bash path. The HTTP/streaming wire format is the
// production surface; the driver is just a convenience.

package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Stub struct {
	mu        sync.Mutex
	sandboxes map[string]*Sandbox

	// Exposed for tests so they can replay what the API said.
	Calls []string
}

func NewStub() *Stub {
	return &Stub{sandboxes: make(map[string]*Sandbox)}
}

// record appends to the call log. Takes the lock briefly to keep it
// concurrency-safe; callers must NOT already hold s.mu (re-entrant lock
// acquisition was the original bug here).
func (s *Stub) record(format string, args ...any) {
	entry := fmt.Sprintf(format, args...)
	s.mu.Lock()
	s.Calls = append(s.Calls, entry)
	s.mu.Unlock()
}

func (s *Stub) Create(ctx context.Context, in CreateInput) (*Sandbox, error) {
	if in.OwnerID == "" {
		return nil, ErrInvalid
	}
	sb := &Sandbox{
		ID:         "stub-" + uuid.NewString(),
		OwnerID:    in.OwnerID,
		Image:      in.Image,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		CPUShares:  in.CPUShares,
		MemoryMB:   in.MemoryMB,
		NetworkOff: in.NetworkOff,
	}
	s.mu.Lock()
	s.sandboxes[sb.ID] = sb
	s.mu.Unlock()
	s.record("create owner=%s image=%s id=%s", in.OwnerID, in.Image, sb.ID)
	return sb, nil
}

func (s *Stub) Get(ctx context.Context, id string) (*Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.sandboxes[id]
	if !ok {
		return nil, ErrNotFound
	}
	out := *sb
	return &out, nil
}

func (s *Stub) List(ctx context.Context, ownerID string) ([]Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Sandbox, 0)
	for _, sb := range s.sandboxes {
		if ownerID == "" || sb.OwnerID == ownerID {
			out = append(out, *sb)
		}
	}
	return out, nil
}

func (s *Stub) Destroy(ctx context.Context, id string) error {
	s.mu.Lock()
	if _, ok := s.sandboxes[id]; !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.sandboxes, id)
	s.mu.Unlock()
	s.record("destroy id=%s", id)
	return nil
}

func (s *Stub) Exec(ctx context.Context, in ExecInput, out io.Writer) (*ExecResult, error) {
	s.mu.Lock()
	sb, ok := s.sandboxes[in.SandboxID]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	if sb.Status != "running" {
		return nil, fmt.Errorf("sandbox not running: status=%s", sb.Status)
	}
	if len(in.Argv) == 0 {
		return nil, ErrInvalid
	}
	s.record("exec id=%s argv=%v", in.SandboxID, in.Argv)

	// Optional timeout
	cmdCtx := ctx
	var cancel context.CancelFunc
	if in.TimeoutSec > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(in.TimeoutSec)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(cmdCtx, in.Argv[0], in.Argv[1:]...)
	cmd.Stdout = out
	cmd.Stderr = out
	if in.Stdin != nil {
		cmd.Stdin = bytes.NewReader(in.Stdin)
	}
	if in.Workdir != "" {
		cmd.Dir = in.Workdir
	}
	err := cmd.Run()
	res := &ExecResult{ExitCode: cmd.ProcessState.ExitCode()}
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}
	if err != nil && res.ExitCode == 0 && !res.TimedOut {
		// Command never started (e.g. binary not found); surface the
		// driver-level error instead of the meaningless exit code.
		return res, err
	}
	return res, nil
}

func (s *Stub) Pause(ctx context.Context, id string) error {
	s.mu.Lock()
	sb, ok := s.sandboxes[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	sb.Status = "paused"
	s.mu.Unlock()
	s.record("pause id=%s", id)
	return nil
}

func (s *Stub) Resume(ctx context.Context, id string) error {
	s.mu.Lock()
	sb, ok := s.sandboxes[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	sb.Status = "running"
	s.mu.Unlock()
	s.record("resume id=%s", id)
	return nil
}

func (s *Stub) Snapshot(ctx context.Context, id string) (string, error) {
	s.mu.Lock()
	_, ok := s.sandboxes[id]
	s.mu.Unlock()
	if !ok {
		return "", ErrNotFound
	}
	snap := "stub-snap-" + uuid.NewString()
	s.record("snapshot id=%s → %s", id, snap)
	return snap, nil
}
