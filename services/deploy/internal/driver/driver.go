// Package driver — pluggable deployment backends.
//
// A "deployment" in BiuMind is a stable URL pointing at content you handed
// us as a tarball:
//
//   - static    — extracted into a public directory and served via HTTP
//     (production: S3 + CloudFront + ACM; dev: filesystem)
//   - container — docker built + run with a published port
//     (production: BuildKit + private registry + K8s; dev:
//     local docker daemon)
//
// The driver interface is intentionally small. Anything richer (build
// args, secrets, env, custom domains) can be added later without breaking
// the wire format.
package driver

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	KindStatic    = "static"
	KindContainer = "container"
)

// Deployment is the live record returned by Deploy().
type Deployment struct {
	ID        string
	OwnerID   string
	Kind      string // static | container
	Status    string // pending | building | running | failed | destroyed
	URL       string // public URL once Status=running
	Image     string // container only — fully qualified image ref
	CreatedAt time.Time
	UpdatedAt time.Time
	Error     string // populated on Status=failed
}

// Plan is what callers pass to Deploy(). The tarball stream is consumed
// once; drivers MUST not buffer it in memory beyond what they need.
type Plan struct {
	OwnerID string
	Kind    string
	Label   string
	// Tarball: gzipped tar of either the static site (root has index.html)
	// or the build context (root has Dockerfile).
	Tarball io.Reader
	// Container only — the port your app listens on inside the container.
	// Driver maps to a free host port and reflects it in URL.
	ContainerPort int
	// Container only — env vars baked into `docker run -e`.
	Env map[string]string
}

type Driver interface {
	Deploy(ctx context.Context, p Plan) (*Deployment, error)
	Get(ctx context.Context, id string) (*Deployment, error)
	List(ctx context.Context, ownerID string) ([]Deployment, error)
	Destroy(ctx context.Context, id string) error
	// Logs streams the build / runtime logs for a deployment. The driver
	// closes `out` when there's nothing more to send (build ended for
	// static; container exited for container kind).
	Logs(ctx context.Context, id string, out io.Writer) error
}

var (
	ErrNotFound = errors.New("deploy: not found")
	ErrInvalid  = errors.New("deploy: invalid argument")
)
