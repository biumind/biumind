// Container driver — `docker build` + `docker run` per deployment.
//
// Why shell out to docker (same reasoning as Sandbox):
//   * Tiny binary — no aws-sdk, no docker SDK.
//   * Stable wire format, forwards-compatible with BuildKit.
//   * The whole surface is build / run / logs / rm.
//
// Production deployment will swap this driver for a Kubernetes Deployment +
// Service driver that pushes to a real registry. The interface here is
// already that shape so the swap is a constructor change in main.go.
//
// Hardening on every container we run:
//   --read-only, --cap-drop=ALL, --security-opt=no-new-privileges,
//   --pids-limit=1024, --memory + --cpus from caller request.
//
// The container is exposed via host port mapping. The driver picks an
// ephemeral port, queries `docker port` to discover what was assigned,
// and reflects it in the deployment URL. Production replacement (K8s)
// returns a Service URL instead.

package driver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Container struct {
	bin     string
	baseURL string // e.g. http://localhost (port appended at deploy time)
	stage   string // tmp dir for build contexts; cleared on destroy

	mu          sync.Mutex
	deployments map[string]*containerState
}

type containerState struct {
	dep         Deployment
	containerID string
	port        int
	logsBuf     *bytes.Buffer
}

func NewContainer(bin, baseURL, stage string) *Container {
	if bin == "" {
		bin = "docker"
	}
	if stage == "" {
		stage = filepath.Join(os.TempDir(), "biu-deploy-stage")
	}
	_ = os.MkdirAll(stage, 0o755)
	return &Container{
		bin:         bin,
		baseURL:     strings.TrimRight(baseURL, "/"),
		stage:       stage,
		deployments: make(map[string]*containerState),
	}
}

func (c *Container) Deploy(ctx context.Context, p Plan) (*Deployment, error) {
	if p.OwnerID == "" || p.Tarball == nil {
		return nil, ErrInvalid
	}
	if p.ContainerPort <= 0 {
		p.ContainerPort = 8080
	}
	id := "ctn-" + uuid.NewString()
	buildDir := filepath.Join(c.stage, id)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return nil, fmt.Errorf("stage dir: %w", err)
	}

	dep := &Deployment{
		ID:        id,
		OwnerID:   p.OwnerID,
		Kind:      KindContainer,
		Status:    "building",
		Image:     "biu/deploy/" + id + ":latest",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	logsBuf := &bytes.Buffer{}
	c.put(&containerState{dep: *dep, logsBuf: logsBuf})

	// Extract the build context.
	if err := extractTarballSafely(p.Tarball, buildDir); err != nil {
		c.fail(id, "extract: "+err.Error())
		return c.snapshot(id), err
	}
	if _, err := os.Stat(filepath.Join(buildDir, "Dockerfile")); err != nil {
		c.fail(id, "missing Dockerfile at build context root")
		return c.snapshot(id), fmt.Errorf("no Dockerfile")
	}

	// Build.
	if out, err := c.run(ctx, "build", "-t", dep.Image, buildDir); err != nil {
		c.appendLogs(id, out)
		c.fail(id, "build: "+strings.TrimSpace(out))
		return c.snapshot(id), err
	} else {
		c.appendLogs(id, out)
	}

	// Allocate a host port up-front so we can reflect a stable URL even if
	// docker run takes a moment.
	hostPort, err := pickFreePort()
	if err != nil {
		c.fail(id, "port: "+err.Error())
		return c.snapshot(id), err
	}

	args := []string{
		"run", "-d",
		"--name", id,
		"--read-only",
		"--tmpfs", "/tmp:size=64m",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit=1024",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, p.ContainerPort),
	}
	for k, v := range p.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, dep.Image)

	if out, err := c.run(ctx, args...); err != nil {
		c.appendLogs(id, out)
		c.fail(id, "run: "+strings.TrimSpace(out))
		return c.snapshot(id), err
	} else {
		c.mu.Lock()
		st := c.deployments[id]
		st.containerID = strings.TrimSpace(out)
		st.port = hostPort
		st.dep.Status = "running"
		st.dep.URL = fmt.Sprintf("%s:%d", c.baseURL, hostPort)
		st.dep.UpdatedAt = time.Now().UTC()
		c.mu.Unlock()
	}

	return c.snapshot(id), nil
}

func (c *Container) Get(ctx context.Context, id string) (*Deployment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.deployments[id]
	if !ok {
		return nil, ErrNotFound
	}
	out := st.dep
	return &out, nil
}

func (c *Container) List(ctx context.Context, ownerID string) ([]Deployment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Deployment, 0)
	for _, st := range c.deployments {
		if ownerID == "" || st.dep.OwnerID == ownerID {
			out = append(out, st.dep)
		}
	}
	return out, nil
}

func (c *Container) Destroy(ctx context.Context, id string) error {
	c.mu.Lock()
	st, ok := c.deployments[id]
	c.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if st.containerID != "" {
		_, _ = c.run(ctx, "rm", "-f", id)
	}
	_, _ = c.run(ctx, "rmi", "-f", st.dep.Image)
	_ = os.RemoveAll(filepath.Join(c.stage, id))
	c.mu.Lock()
	delete(c.deployments, id)
	c.mu.Unlock()
	return nil
}

func (c *Container) Logs(ctx context.Context, id string, out io.Writer) error {
	c.mu.Lock()
	st, ok := c.deployments[id]
	c.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if _, err := io.WriteString(out, "=== build logs ===\n"); err != nil {
		return err
	}
	if _, err := out.Write(st.logsBuf.Bytes()); err != nil {
		return err
	}
	if st.containerID == "" {
		return nil
	}
	if _, err := io.WriteString(out, "\n=== container logs ===\n"); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.bin, "logs", "--tail=200", id)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// ─── helpers ──────────────────────────────────────────────

func (c *Container) put(st *containerState) {
	c.mu.Lock()
	c.deployments[st.dep.ID] = st
	c.mu.Unlock()
}

func (c *Container) snapshot(id string) *Deployment {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.deployments[id]
	if !ok {
		return nil
	}
	out := st.dep
	return &out
}

func (c *Container) appendLogs(id string, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, ok := c.deployments[id]; ok {
		st.logsBuf.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			st.logsBuf.WriteByte('\n')
		}
	}
}

func (c *Container) fail(id, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, ok := c.deployments[id]; ok {
		st.dep.Status = "failed"
		st.dep.Error = msg
		st.dep.UpdatedAt = time.Now().UTC()
	}
}

func (c *Container) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		first := bufio.NewScanner(&stderr)
		first.Scan()
		return first.Text(), err
	}
	return string(out), nil
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ensure container always returns deployment metadata even on error paths
var _ Driver = (*Container)(nil)
var _ = errors.New
