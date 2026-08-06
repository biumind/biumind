// Blob unit tests against a real MinIO. Skips when MinIO is unreachable
// — same convention as api_test.go (laptop defaults match docker-compose).
//
// Covers Put/Get/Head/Remove plus the new Presign* helpers (R1 spike
// codified as a regression test).

package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

const blobTestBucket = "biumind-files-test"

func newTestBlob(t *testing.T) *Blob {
	t.Helper()
	mEndpoint := os.Getenv("MINIO_ENDPOINT")
	mAccess := os.Getenv("MINIO_ACCESS_KEY")
	mSecret := os.Getenv("MINIO_SECRET_KEY")
	if mEndpoint == "" {
		mEndpoint = "localhost:9000"
		mAccess = "biumind"
		mSecret = "biumind_minio_dev"
	}
	blob, err := NewBlob(context.Background(), BlobConfig{
		Endpoint:     mEndpoint,
		AccessKey:    mAccess,
		SecretKey:    mSecret,
		Bucket:       blobTestBucket,
		EnsureBucket: true,
	})
	if err != nil {
		t.Skipf("MinIO connect failed (skip): %v", err)
	}
	return blob
}

func randomKey(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s/%x-%d", prefix, b, time.Now().UnixNano())
}

// ─── Put / Get / Head / Remove round-trip ───────────────────

func TestBlobPutGetHeadRemove(t *testing.T) {
	blob := newTestBlob(t)
	ctx := context.Background()
	key := randomKey("blob-roundtrip")
	body := []byte("hello-blob-世界")
	t.Cleanup(func() { _ = blob.Remove(ctx, key) })

	if err := blob.Put(ctx, key, bytes.NewReader(body), int64(len(body)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	info, err := blob.Head(ctx, key)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if info.Size != int64(len(body)) {
		t.Errorf("Head size: got %d want %d", info.Size, len(body))
	}
	if info.ContentType != "text/plain" {
		t.Errorf("Head content-type: got %q want text/plain", info.ContentType)
	}

	rc, _, err := blob.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("Get read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body mismatch: got %q want %q", got, body)
	}

	if err := blob.Remove(ctx, key); err != nil {
		t.Errorf("Remove: %v", err)
	}
	if _, err := blob.Head(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Head after remove: got %v want ErrNotFound", err)
	}
}

// ─── PresignPut: locks Content-Type ─────────────────────────
// Codifies the R1 spike: Content-Type baked into the signature, MinIO
// rejects requests that PUT a different (or missing) Content-Type.

func TestBlobPresignPutLocksContentType(t *testing.T) {
	blob := newTestBlob(t)
	ctx := context.Background()
	key := randomKey("presign-put")
	t.Cleanup(func() { _ = blob.Remove(ctx, key) })

	signed, err := blob.PresignPut(ctx, key, 5*time.Minute, "image/png")
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	body := []byte("not-really-png")

	cases := []struct {
		name        string
		contentType string
		wantOK      bool
	}{
		{"matching CT", "image/png", true},
		{"wrong CT", "application/octet-stream", false},
		{"empty CT (auto-detected by Go)", "", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPut, signed.String(), bytes.NewReader(body))
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			req.ContentLength = int64(len(body))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("PUT: %v", err)
			}
			defer resp.Body.Close()
			ok := resp.StatusCode < 400
			if ok != c.wantOK {
				slurp, _ := io.ReadAll(resp.Body)
				t.Errorf("status=%d wantOK=%v body=%s", resp.StatusCode, c.wantOK, slurp)
			}
		})
	}
}

// ─── PresignPut: TTL is honored ─────────────────────────────
// Hard to test without time travel; cheap proxy: a 1-second TTL URL
// works immediately, fails after a 2-second sleep.

func TestBlobPresignPutTTLExpires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping TTL expiry test in -short mode (sleeps 2s)")
	}
	blob := newTestBlob(t)
	ctx := context.Background()
	key := randomKey("presign-ttl")
	t.Cleanup(func() { _ = blob.Remove(ctx, key) })

	signed, err := blob.PresignPut(ctx, key, 1*time.Second, "text/plain")
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	time.Sleep(2 * time.Second)

	req, _ := http.NewRequest(http.MethodPut, signed.String(), bytes.NewReader([]byte("late")))
	req.Header.Set("Content-Type", "text/plain")
	req.ContentLength = 4
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("expired URL accepted: status=%d", resp.StatusCode)
	}
}

// ─── PresignGet: round-trip ────────────────────────────────

func TestBlobPresignGetRoundTrip(t *testing.T) {
	blob := newTestBlob(t)
	ctx := context.Background()
	key := randomKey("presign-get")
	body := []byte("presigned-download-payload")
	t.Cleanup(func() { _ = blob.Remove(ctx, key) })
	if err := blob.Put(ctx, key, bytes.NewReader(body), int64(len(body)), "application/octet-stream"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	signed, err := blob.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	resp, err := http.Get(signed.String())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET status: %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, body) {
		t.Errorf("body mismatch")
	}
}

// ─── Head: missing object → ErrNotFound ────────────────────

func TestBlobHeadMissing(t *testing.T) {
	blob := newTestBlob(t)
	ctx := context.Background()
	_, err := blob.Head(ctx, randomKey("missing"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Head missing: got %v want ErrNotFound", err)
	}
}
