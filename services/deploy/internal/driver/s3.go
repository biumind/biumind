// S3-compatible driver — extracts the uploaded tarball into a per-deploy
// "key prefix" inside an S3 bucket, returns a public URL.
//
// Works against:
//   - AWS S3 (when DEPLOY_S3_REGION + access keys are set)
//   - MinIO (already running locally on :9000)
//   - Cloudflare R2, Backblaze B2, Wasabi, etc — anything that speaks
//     the s3 PutObject API and accepts SigV4.
//
// We sign requests with AWS SigV4 directly rather than pulling
// aws-sdk-go-v2 (≈40MB of indirect deps). The handful of headers we
// touch (`Host`, `Content-Length`, `x-amz-content-sha256`,
// `x-amz-date`, `Authorization`) is the entire surface.
//
// Public URL composition:
//
//	<DEPLOY_S3_PUBLIC_URL>/<bucket>/<key>      path-style (default; MinIO)
//	<DEPLOY_S3_PUBLIC_URL>/<key>               vhost-style (set
//	    DEPLOY_S3_VHOST=true when the bucket lives at the apex domain)
package driver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type S3 struct {
	Endpoint  string // https://s3.amazonaws.com  or  http://localhost:9000
	Region    string // us-east-1 (MinIO accepts anything)
	Bucket    string
	AccessKey string
	SecretKey string
	PublicURL string // serving prefix; reflected back in Deployment.URL
	Vhost     bool   // true → bucket already part of host; key joined direct
	Client    *http.Client

	mu          sync.Mutex
	deployments map[string]*Deployment
}

func NewS3(endpoint, region, bucket, accessKey, secretKey, publicURL string, vhost bool) *S3 {
	if region == "" {
		region = "us-east-1"
	}
	if publicURL == "" {
		publicURL = endpoint
	}
	return &S3{
		Endpoint:    strings.TrimRight(endpoint, "/"),
		Region:      region,
		Bucket:      bucket,
		AccessKey:   accessKey,
		SecretKey:   secretKey,
		PublicURL:   strings.TrimRight(publicURL, "/"),
		Vhost:       vhost,
		Client:      &http.Client{Timeout: 60 * time.Second},
		deployments: map[string]*Deployment{},
	}
}

func (s *S3) Deploy(ctx context.Context, p Plan) (*Deployment, error) {
	if p.OwnerID == "" || p.Tarball == nil {
		return nil, ErrInvalid
	}
	id := "s3-" + uuid.NewString()

	dep := &Deployment{
		ID:        id,
		OwnerID:   p.OwnerID,
		Kind:      KindStatic,
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	// Read the gzipped tar streaming and PUT each entry directly.
	gz, err := gzip.NewReader(p.Tarball)
	if err != nil {
		dep.Status = "failed"
		dep.Error = "gzip: " + err.Error()
		s.put(dep)
		return dep, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			dep.Status = "failed"
			dep.Error = "tar: " + err.Error()
			s.put(dep)
			return dep, err
		}
		if !isSafeTarEntry(hdr.Name) {
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		// Buffer per-entry. 100MB cap matches Static driver.
		if hdr.Size > maxStaticEntryBytes {
			dep.Status = "failed"
			dep.Error = fmt.Sprintf("entry %s too large", hdr.Name)
			s.put(dep)
			return dep, fmt.Errorf("entry too large: %s", hdr.Name)
		}
		buf := bytes.Buffer{}
		if _, err := io.CopyN(&buf, tr, hdr.Size); err != nil {
			dep.Status = "failed"
			dep.Error = "read entry: " + err.Error()
			s.put(dep)
			return dep, err
		}
		key := id + "/" + strings.TrimLeft(hdr.Name, "./")
		if err := s.putObject(ctx, key, buf.Bytes(), guessContentType(hdr.Name)); err != nil {
			dep.Status = "failed"
			dep.Error = "put: " + err.Error()
			s.put(dep)
			return dep, err
		}
	}

	if s.Vhost {
		dep.URL = s.PublicURL + "/" + id + "/"
	} else {
		dep.URL = s.PublicURL + "/" + s.Bucket + "/" + id + "/"
	}
	s.put(dep)
	return dep, nil
}

func (s *S3) Get(ctx context.Context, id string) (*Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deployments[id]
	if !ok {
		return nil, ErrNotFound
	}
	out := *d
	return &out, nil
}

func (s *S3) List(ctx context.Context, ownerID string) ([]Deployment, error) {
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

func (s *S3) Destroy(ctx context.Context, id string) error {
	// MVP: just drop our local record. Bucket cleanup is left to a
	// lifecycle policy (e.g. expire `s3-*/` after 30 days). Production
	// implementation should issue a ListObjectsV2 + DeleteObjects loop.
	s.mu.Lock()
	if _, ok := s.deployments[id]; !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.deployments, id)
	s.mu.Unlock()
	return nil
}

func (s *S3) Logs(ctx context.Context, id string, w io.Writer) error {
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.Error != "" {
		_, _ = io.WriteString(w, "deploy failed: "+d.Error+"\n")
	} else {
		_, _ = fmt.Fprintf(w, "deploy succeeded; static files served at %s\n", d.URL)
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────

func (s *S3) put(d *Deployment) {
	s.mu.Lock()
	s.deployments[d.ID] = d
	s.mu.Unlock()
}

// putObject signs and PUTs a single key. SigV4 over the SHA256-hashed
// payload is the simplest scheme that covers AWS + every S3-compatible
// implementation.
func (s *S3) putObject(ctx context.Context, key string, body []byte, contentType string) error {
	u := fmt.Sprintf("%s/%s/%s", s.Endpoint, s.Bucket, key)
	if s.Vhost {
		u = fmt.Sprintf("%s/%s", s.Endpoint, key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", contentType)

	if err := s.signV4(req, body); err != nil {
		return err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("s3 put %s -> %d: %s", key, resp.StatusCode, string(respBody))
	}
	return nil
}

// signV4 implements the bare minimum of AWS SigV4 needed for PutObject:
//  1. canonical request from method+path+query+headers+sha256(body)
//  2. string-to-sign from <algo>+<datetime>+<scope>+sha256(canonical)
//  3. signing key = HMAC-SHA256 chain (date → region → service → request)
//  4. Authorization header reflecting all of the above.
func (s *S3) signV4(req *http.Request, body []byte) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	scopeDate := now.Format("20060102")
	scope := scopeDate + "/" + s.Region + "/s3/aws4_request"
	bodySha := hashSHA256(body)

	host := req.URL.Host
	req.Header.Set("Host", host)
	req.Header.Set("x-amz-content-sha256", bodySha)
	req.Header.Set("x-amz-date", amzDate)

	// Sign these headers (lowercase alphabetic) — host + content-sha256
	// + date are mandatory; we add content-type when present so the
	// server treats the body as expected.
	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("Content-Type") != "" {
		signed = append(signed, "content-type")
	}
	canonHeaders := canonicalHeaders(req.Header, signed)
	canonReq := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonHeaders,
		strings.Join(signed, ";"),
		bodySha,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashSHA256([]byte(canonReq)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+s.SecretKey), []byte(scopeDate))
	kRegion := hmacSHA256(kDate, []byte(s.Region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.AccessKey, scope, strings.Join(signed, ";"), signature,
	)
	req.Header.Set("Authorization", authHeader)
	return nil
}

func canonicalHeaders(h http.Header, signed []string) string {
	var b strings.Builder
	for _, name := range signed {
		v := strings.TrimSpace(strings.Join(h.Values(strings.Title(name)), ","))
		if v == "" {
			v = strings.TrimSpace(h.Get(name))
		}
		b.WriteString(name + ":" + v + "\n")
	}
	return b.String()
}

func hashSHA256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func isSafeTarEntry(name string) bool {
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return false
	}
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if seg == ".." {
			return false
		}
	}
	return true
}

func guessContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".wasm"):
		return "application/wasm"
	default:
		return "application/octet-stream"
	}
}

// suppress unused import warning when net/url drops out of the file.
var _ = url.Parse
