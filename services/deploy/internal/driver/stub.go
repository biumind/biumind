// Stub driver — in-memory, no real deployment work.
//
// Used for unit tests of the HTTP API and as an option for CI runners
// that can't reach Docker. Tarball is consumed (drained) so size limits
// in the API layer still get exercised.

package driver

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Stub struct {
	mu          sync.Mutex
	deployments map[string]*Deployment
	logs        map[string]string

	// Test hook — set to true to make every Deploy() return a failed
	// deployment, so the API layer's failure path can be exercised.
	FailNext bool
}

func NewStub() *Stub {
	return &Stub{
		deployments: make(map[string]*Deployment),
		logs:        make(map[string]string),
	}
}

func (s *Stub) Deploy(ctx context.Context, p Plan) (*Deployment, error) {
	if p.OwnerID == "" {
		return nil, ErrInvalid
	}
	if p.Tarball != nil {
		// Drain the tarball so the multipart parser advances. We don't
		// persist anything — just count bytes for the test to assert on.
		n, _ := io.Copy(io.Discard, p.Tarball)
		_ = n
	}
	id := "stub-" + uuid.NewString()
	dep := &Deployment{
		ID:        id,
		OwnerID:   p.OwnerID,
		Kind:      p.Kind,
		Status:    "running",
		URL:       fmt.Sprintf("https://stub.local/%s/", id),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if s.FailNext {
		dep.Status = "failed"
		dep.Error = "FailNext=true"
		s.FailNext = false
	}
	s.mu.Lock()
	s.deployments[id] = dep
	s.logs[id] = "stub deploy " + id + "\n"
	s.mu.Unlock()
	return dep, nil
}

func (s *Stub) Get(ctx context.Context, id string) (*Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deployments[id]
	if !ok {
		return nil, ErrNotFound
	}
	out := *d
	return &out, nil
}

func (s *Stub) List(ctx context.Context, ownerID string) ([]Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Deployment, 0)
	for _, d := range s.deployments {
		if ownerID == "" || d.OwnerID == ownerID {
			out = append(out, *d)
		}
	}
	return out, nil
}

func (s *Stub) Destroy(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deployments[id]; !ok {
		return ErrNotFound
	}
	delete(s.deployments, id)
	delete(s.logs, id)
	return nil
}

func (s *Stub) Logs(ctx context.Context, id string, out io.Writer) error {
	s.mu.Lock()
	logs, ok := s.logs[id]
	s.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	_, err := io.WriteString(out, logs)
	return err
}
