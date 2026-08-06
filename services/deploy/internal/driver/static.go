// Static driver — extracts the uploaded tarball into a per-deployment
// directory under DEPLOY_STATIC_ROOT and exposes it via the deploy
// service's embedded HTTP file server at /static/{id}/.
//
// Production deployment will swap this driver for an S3 backend (uploads
// to a bucket fronted by CloudFront + ACM). The interface stays the same
// so callers don't notice.
//
// Hardening:
//   * tar entries with `..` or absolute paths are rejected at extract
//     time so we don't write outside the deploy dir.
//   * symlinks are dropped (extracted as nothing) — they're a stored-XSS
//     risk on a public file server.
//   * each entry is capped at 100 MB; total deployment size capped at 1 GB.

package driver

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	maxStaticEntryBytes = 100 << 20 // 100 MB per file
	maxStaticTotalBytes = 1 << 30   // 1 GB per deployment
)

type Static struct {
	root    string // base dir; per-deploy lives at root/<id>/
	baseURL string // public URL prefix; deploy URL is baseURL + "/" + id + "/"

	mu          sync.Mutex
	deployments map[string]*Deployment
}

func NewStatic(root, baseURL string) *Static {
	return &Static{
		root:        root,
		baseURL:     strings.TrimRight(baseURL, "/"),
		deployments: make(map[string]*Deployment),
	}
}

// Root returns the base directory; the HTTP layer mounts a FileServer here.
func (s *Static) Root() string { return s.root }

func (s *Static) Deploy(ctx context.Context, p Plan) (*Deployment, error) {
	if p.OwnerID == "" || p.Tarball == nil {
		return nil, ErrInvalid
	}
	id := "stx-" + uuid.NewString()
	dir := filepath.Join(s.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	dep := &Deployment{
		ID:        id,
		OwnerID:   p.OwnerID,
		Kind:      KindStatic,
		Status:    "running",
		URL:       s.baseURL + "/" + id + "/",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := extractTarballSafely(p.Tarball, dir); err != nil {
		_ = os.RemoveAll(dir)
		dep.Status = "failed"
		dep.Error = err.Error()
		s.put(dep)
		return dep, fmt.Errorf("extract: %w", err)
	}

	s.put(dep)
	return dep, nil
}

func (s *Static) Get(ctx context.Context, id string) (*Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deployments[id]
	if !ok {
		return nil, ErrNotFound
	}
	out := *d
	return &out, nil
}

func (s *Static) List(ctx context.Context, ownerID string) ([]Deployment, error) {
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

func (s *Static) Destroy(ctx context.Context, id string) error {
	s.mu.Lock()
	d, ok := s.deployments[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.deployments, id)
	s.mu.Unlock()
	d.Status = "destroyed"
	return os.RemoveAll(filepath.Join(s.root, id))
}

func (s *Static) Logs(ctx context.Context, id string, out io.Writer) error {
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.Error != "" {
		_, _ = io.WriteString(out, "deploy failed: "+d.Error+"\n")
	} else {
		_, _ = io.WriteString(out, "deploy succeeded; static files served at "+d.URL+"\n")
	}
	return nil
}

func (s *Static) put(d *Deployment) {
	s.mu.Lock()
	s.deployments[d.ID] = d
	s.mu.Unlock()
}

// extractTarballSafely reads a gzipped tar from r and writes regular files
// only into dir. Reject paths that escape dir, symlinks, and any single
// entry > maxStaticEntryBytes or running total > maxStaticTotalBytes.
func extractTarballSafely(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// Reject the raw entry name if it contains any `..` segment or is
		// absolute. filepath.Clean would happily collapse `../foo` into
		// `/foo` and still extract — we want a hard reject so attackers
		// can't smuggle path-escape attempts into a successful deploy.
		raw := hdr.Name
		if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) {
			return fmt.Errorf("absolute path not allowed: %s", raw)
		}
		for _, seg := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == ".." {
				return fmt.Errorf("unsafe path segment: %s", raw)
			}
		}
		clean := filepath.Clean(raw)
		dest := filepath.Join(dir, clean)
		if !strings.HasPrefix(dest, dir+string(os.PathSeparator)) && dest != dir {
			return fmt.Errorf("path escape: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size > maxStaticEntryBytes {
				return fmt.Errorf("entry %s too large: %d > %d", hdr.Name, hdr.Size, maxStaticEntryBytes)
			}
			total += hdr.Size
			if total > maxStaticTotalBytes {
				return fmt.Errorf("deployment too large (>%d bytes)", maxStaticTotalBytes)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(f, tr, hdr.Size); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink, tar.TypeLink:
			// Drop. Public file server + symlinks = stored XSS / SSRF.
			continue
		default:
			// Block devices, fifos, etc — drop silently.
			continue
		}
	}
}
