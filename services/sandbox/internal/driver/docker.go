// Docker driver — `docker run --network=none` per sandbox + `docker exec`
// per command.
//
// Why shell out to the docker CLI instead of pulling github.com/docker/docker?
//
//   * Single Go binary stays small (~15 MB → ~150 MB with the SDK).
//   * Stable wire format: docker CLI 24+ has been backwards-compatible
//     for the subset we touch (run / exec / kill / inspect).
//   * The whole container surface is a few dozen lines — the SDK's value
//     is in event streams + low-latency build APIs we don't use.
//
// Hardening defaults applied to every sandbox:
//
//   --network=none            no egress unless explicitly allowed
//   --read-only               root fs is read-only; /tmp is a tmpfs
//   --tmpfs /tmp:size=64m
//   --cap-drop=ALL            no Linux capabilities
//   --security-opt=no-new-privileges
//   --pids-limit=512          fork-bomb floor
//   --memory / --cpus         from CreateInput
//
// Egress allowlist — when the caller passes EgressAllow, we connect the
// sandbox to a per-service bridge network (`biu-sbx-egress`) and rely on
// the daemon's existing iptables to allow only those host:port pairs. The
// rule writing happens out-of-band (host setup script); the driver only
// flips --network when the list is non-empty. For MVP we just log a
// warning that the allowlist is not enforced unless the host has been
// pre-configured.

package driver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const dockerLabelOwner = "biu.sandbox.owner"

type Docker struct {
	bin    string // path to docker CLI; defaults to "docker"
	logger *slog.Logger
	policy Policy // R5: image allowlist / egress enforce / workspace tmpfs / non-root

	mu sync.Mutex
	// Local mirror of metadata we need to round-trip (owner_id is stored
	// as a label so it survives daemon restarts; everything else we keep
	// in-memory because it's hot-path).
	cache map[string]*Sandbox
}

func NewDocker(bin string, logger *slog.Logger, policy Policy) *Docker {
	if bin == "" {
		bin = "docker"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Docker{bin: bin, logger: logger, policy: policy, cache: make(map[string]*Sandbox)}
}

func (d *Docker) Create(ctx context.Context, in CreateInput) (*Sandbox, error) {
	if in.OwnerID == "" {
		return nil, ErrInvalid
	}
	// R5: 镜像白名单——空→默认；非白名单→拒绝。
	img, err := d.policy.ResolveImage(in.Image)
	if err != nil {
		return nil, err
	}
	in.Image = img

	id := "sbx-" + uuid.NewString()
	args := []string{
		"run", "-d",
		"--name", id,
		"--label", dockerLabelOwner + "=" + in.OwnerID,
		"--read-only",
		"--tmpfs", "/tmp:size=64m,exec",
		// R5: /workspace 可写 tmpfs（rootfs 只读 → 否则 workdir=/workspace 写失败）。
		"--tmpfs", fmt.Sprintf("/workspace:size=%dm,exec", d.policy.workspaceMB()),
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit=512",
	}
	// R5: 非 root user（空 = 保留 image 默认/root 逃生门）。/tmp + /workspace
	// tmpfs 世界可写,非 root 能跑读写命令。
	if d.policy.RunAsUser != "" {
		args = append(args, "--user", d.policy.RunAsUser)
	}

	// Network policy（R5: egress fail-closed）
	switch {
	case in.NetworkOff || len(in.EgressAllow) == 0:
		args = append(args, "--network=none")
	case d.policy.EgressEnforced:
		// 运维确认 host 侧 biu-sbx-egress 桥 + iptables 已就位（显式 opt-in）。
		args = append(args, "--network=biu-sbx-egress")
		d.logger.Info("egress: selective egress via enforced bridge",
			"sandbox", id, "allow", in.EgressAllow)
	default:
		// fail-closed：未启用 egress enforcement 时,不连不受控的 bridge
		// （那会给假的"受限"安全）——降级到无网络。
		args = append(args, "--network=none")
		d.logger.Warn("egress: selective egress requested but SANDBOX_EGRESS_ENFORCED=false; failing closed to network=none",
			"sandbox", id, "allow", in.EgressAllow)
	}

	if in.CPUShares > 0 {
		// 1024 ≈ 1 core.
		args = append(args, "--cpus", strconv.FormatFloat(float64(in.CPUShares)/1024.0, 'f', 2, 64))
	}
	if in.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(in.MemoryMB)+"m")
	}
	for k, v := range in.Env {
		args = append(args, "-e", k+"="+v)
	}

	args = append(args, in.Image, "sleep", "infinity")

	out, err := d.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w (%s)", err, strings.TrimSpace(out))
	}

	sb := &Sandbox{
		ID:         id,
		OwnerID:    in.OwnerID,
		Image:      in.Image,
		Status:     "running",
		CreatedAt:  time.Now().UTC(),
		CPUShares:  in.CPUShares,
		MemoryMB:   in.MemoryMB,
		NetworkOff: in.NetworkOff,
	}
	d.mu.Lock()
	d.cache[id] = sb
	d.mu.Unlock()
	return sb, nil
}

func (d *Docker) Get(ctx context.Context, id string) (*Sandbox, error) {
	d.mu.Lock()
	sb, ok := d.cache[id]
	d.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	out := *sb
	return &out, nil
}

func (d *Docker) List(ctx context.Context, ownerID string) ([]Sandbox, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Sandbox, 0)
	for _, sb := range d.cache {
		if ownerID == "" || sb.OwnerID == ownerID {
			out = append(out, *sb)
		}
	}
	return out, nil
}

func (d *Docker) Destroy(ctx context.Context, id string) error {
	d.mu.Lock()
	_, ok := d.cache[id]
	d.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	// `docker rm -f` covers both running + exited.
	if out, err := d.run(ctx, "rm", "-f", id); err != nil {
		return fmt.Errorf("docker rm: %w (%s)", err, strings.TrimSpace(out))
	}
	d.mu.Lock()
	delete(d.cache, id)
	d.mu.Unlock()
	return nil
}

func (d *Docker) Exec(ctx context.Context, in ExecInput, w io.Writer) (*ExecResult, error) {
	d.mu.Lock()
	sb, ok := d.cache[in.SandboxID]
	d.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	if sb.Status != "running" {
		return nil, fmt.Errorf("sandbox not running: status=%s", sb.Status)
	}
	if len(in.Argv) == 0 {
		return nil, ErrInvalid
	}

	cmdCtx := ctx
	var cancel context.CancelFunc
	if in.TimeoutSec > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(in.TimeoutSec)*time.Second)
		defer cancel()
	}

	args := []string{"exec", "-i"}
	if in.Workdir != "" {
		args = append(args, "-w", in.Workdir)
	}
	args = append(args, in.SandboxID)
	args = append(args, in.Argv...)

	cmd := exec.CommandContext(cmdCtx, d.bin, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if in.Stdin != nil {
		cmd.Stdin = bytes.NewReader(in.Stdin)
	}

	err := cmd.Run()
	res := &ExecResult{ExitCode: cmd.ProcessState.ExitCode()}
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}
	// `docker exec` propagates the inner exit code, so a non-zero exit
	// from a successful exec isn't an error from our perspective.
	if err != nil && res.ExitCode == 0 && !res.TimedOut {
		return res, err
	}
	return res, nil
}

func (d *Docker) Pause(ctx context.Context, id string) error {
	if _, err := d.Get(ctx, id); err != nil {
		return err
	}
	if out, err := d.run(ctx, "pause", id); err != nil {
		return fmt.Errorf("docker pause: %w (%s)", err, strings.TrimSpace(out))
	}
	d.mu.Lock()
	d.cache[id].Status = "paused"
	d.mu.Unlock()
	return nil
}

func (d *Docker) Resume(ctx context.Context, id string) error {
	if _, err := d.Get(ctx, id); err != nil {
		return err
	}
	if out, err := d.run(ctx, "unpause", id); err != nil {
		return fmt.Errorf("docker unpause: %w (%s)", err, strings.TrimSpace(out))
	}
	d.mu.Lock()
	d.cache[id].Status = "running"
	d.mu.Unlock()
	return nil
}

func (d *Docker) Snapshot(ctx context.Context, id string) (string, error) {
	if _, err := d.Get(ctx, id); err != nil {
		return "", err
	}
	tag := "biu/sbx-snap:" + uuid.NewString()
	if out, err := d.run(ctx, "commit", id, tag); err != nil {
		return "", fmt.Errorf("docker commit: %w (%s)", err, strings.TrimSpace(out))
	}
	return tag, nil
}

func (d *Docker) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, d.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Surface the first stderr line — that's where docker writes the
		// real reason (image not found, daemon unreachable, etc.).
		first := bufio.NewScanner(&stderr)
		first.Scan()
		return first.Text(), err
	}
	return string(out), nil
}
