// HTTP handler tests for the files package — wires Store + Blob (real MinIO)
// + Verifier and exercises the routes via httptest.
//
// Skips when DATABASE_URL or MINIO_ENDPOINT unset (laptop without docker).
//
// Test bucket isolated from prod: biumind-files-test (auto-created by
// EnsureBucket).

package files

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testJWTSecret   = "biumind-files-api-test-secret-32+ch"
	testJWTIssuer   = "https://identity.test"
	testJWTAudience = "biumind-api"
	testBucket      = "biumind-files-test"
)

type apiHarness struct {
	server *httptest.Server
	signer *bauth.Signer
	pool   *pgxpool.Pool
	blob   *Blob
}

func (h *apiHarness) close() {
	h.server.Close()
	h.pool.Close()
}

func (h *apiHarness) mintToken(uid uuid.UUID) string {
	tok, err := h.signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		panic(err)
	}
	return tok
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	mEndpoint := os.Getenv("MINIO_ENDPOINT")
	mAccess := os.Getenv("MINIO_ACCESS_KEY")
	mSecret := os.Getenv("MINIO_SECRET_KEY")
	if mEndpoint == "" {
		// laptop convenience defaults — match docker-compose
		mEndpoint = "localhost:9000"
		mAccess = "biumind"
		mSecret = "biumind_minio_dev"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	blob, err := NewBlob(context.Background(), BlobConfig{
		Endpoint:     mEndpoint,
		AccessKey:    mAccess,
		SecretKey:    mSecret,
		Bucket:       testBucket,
		EnsureBucket: true,
	})
	if err != nil {
		pool.Close()
		t.Skipf("MinIO connect failed (skip): %v", err)
	}
	srv := &Server{
		Store:          NewStore(pool),
		Blob:           blob,
		Verifier:       bauth.NewVerifier(testJWTSecret, testJWTIssuer, testJWTAudience),
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxUploadBytes: 5 * 1024 * 1024, // 5 MB cap for tests
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	return &apiHarness{
		server: httptest.NewServer(mux),
		signer: bauth.NewSigner(testJWTSecret, testJWTIssuer, testJWTAudience, 5*time.Minute),
		pool:   pool,
		blob:   blob,
	}
}

// ─── Auth ─────────────────────────────────────────────────

func TestFilesAPI_RejectsMissingBearer(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	r, _ := http.NewRequest("POST", h.server.URL+"/v1/files/upload", nil)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing bearer: expected 401 got %d", resp.StatusCode)
	}
}

func TestFilesAPI_RejectsInvalidToken(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	r, _ := http.NewRequest("POST", h.server.URL+"/v1/files/upload", nil)
	r.Header.Set("Authorization", "Bearer not-a-real-jwt")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("invalid token: expected 401 got %d", resp.StatusCode)
	}
}

// ─── Upload + Download round-trip ─────────────────────────

func TestFilesAPI_UploadDownloadRoundTrip(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)

	payload := []byte("hello biumind, this is the file content for round-trip test")
	resp := h.uploadBytes(t, tok, "hello.txt", "text/plain", payload, nil)
	defer resp.body.Close()
	if resp.status != 200 {
		t.Fatalf("upload: %d %s", resp.status, resp.bodyStr)
	}
	if resp.json["id"] == "" || resp.json["sha256"] == "" {
		t.Errorf("upload response missing fields: %+v", resp.json)
	}
	if resp.json["deduped"] != false {
		t.Errorf("first upload should not be deduped: %+v", resp.json)
	}
	fileID := resp.json["id"].(string)
	defer h.cleanupFile(uid, fileID)

	// Download → should match exactly
	dresp, derr := http.NewRequest("GET", h.server.URL+"/v1/files/"+fileID, nil)
	if derr != nil {
		t.Fatalf("req: %v", derr)
	}
	dresp.Header.Set("Authorization", "Bearer "+tok)
	dr, err := http.DefaultClient.Do(dresp)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer dr.Body.Close()
	if dr.StatusCode != 200 {
		bb, _ := io.ReadAll(dr.Body)
		t.Fatalf("download status %d: %s", dr.StatusCode, bb)
	}
	if ct := dr.Header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("download Content-Type: %q want text/plain", ct)
	}
	body, err := io.ReadAll(dr.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("payload mismatch: got %q", string(body))
	}
}

func TestFilesAPI_UploadDedupSameUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)

	payload := []byte("dedup-content " + uuid.New().String())
	r1 := h.uploadBytes(t, tok, "x.bin", "application/octet-stream", payload, nil)
	defer r1.body.Close()
	if r1.status != 200 {
		t.Fatalf("first upload: %d", r1.status)
	}
	id1 := r1.json["id"].(string)
	defer h.cleanupFile(uid, id1)
	if r1.json["deduped"] != false {
		t.Errorf("first upload deduped flag wrong: %+v", r1.json)
	}

	// 第二次同 payload — server 应该返回同 id, deduped=true
	r2 := h.uploadBytes(t, tok, "y.bin", "application/octet-stream", payload, nil)
	defer r2.body.Close()
	if r2.status != 200 {
		t.Fatalf("second upload: %d", r2.status)
	}
	if r2.json["id"] != id1 {
		t.Errorf("dedup should return same id: r1=%s r2=%v", id1, r2.json["id"])
	}
	if r2.json["deduped"] != true {
		t.Errorf("second upload should be deduped: %+v", r2.json)
	}
}

func TestFilesAPI_DedupNotCrossUser(t *testing.T) {
	// 同 sha256 跨用户独立, 各自一份 (隐私 + 配额)。
	h := newAPIHarness(t)
	defer h.close()
	uidA := uuid.New()
	uidB := uuid.New()
	payload := []byte("xross-user-test " + uuid.New().String())

	rA := h.uploadBytes(t, h.mintToken(uidA), "a.txt", "text/plain", payload, nil)
	defer rA.body.Close()
	idA := rA.json["id"].(string)
	defer h.cleanupFile(uidA, idA)

	rB := h.uploadBytes(t, h.mintToken(uidB), "b.txt", "text/plain", payload, nil)
	defer rB.body.Close()
	idB := rB.json["id"].(string)
	defer h.cleanupFile(uidB, idB)

	if idA == idB {
		t.Errorf("cross-user dedup leaked: A=%s B=%s", idA, idB)
	}
	if rA.json["deduped"] == true || rB.json["deduped"] == true {
		t.Errorf("neither upload should be deduped (cross-user)")
	}
}

func TestFilesAPI_UploadRejectsTooLarge(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	tok := h.mintToken(uuid.New())
	big := bytes.Repeat([]byte("a"), int(h.maxUploadBytes())+10)
	r := h.uploadBytes(t, tok, "big.bin", "application/octet-stream", big, nil)
	defer r.body.Close()
	// MaxBytesReader 超限会让 ParseMultipartForm 报 400; tmp file 也可能 500;
	// 任意 4xx/5xx 都算正确拦下 (重点是不让超大文件成功 PutObject)
	if r.status == 200 {
		t.Errorf("oversized upload should fail, got 200 %v", r.json)
	}
}

// ─── Cross-tenant ─────────────────────────────────────────

func TestFilesAPI_DownloadCrossTenant404(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	owner := uuid.New()
	intruder := uuid.New()
	r := h.uploadBytes(t, h.mintToken(owner), "secret.txt", "text/plain",
		[]byte("ssh-rsa AAAA..."), nil)
	defer r.body.Close()
	if r.status != 200 {
		t.Fatalf("seed upload: %d", r.status)
	}
	fileID := r.json["id"].(string)
	defer h.cleanupFile(owner, fileID)

	req, _ := http.NewRequest("GET", h.server.URL+"/v1/files/"+fileID, nil)
	req.Header.Set("Authorization", "Bearer "+h.mintToken(intruder))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-tenant download: expected 404, got %d", resp.StatusCode)
	}
}

func TestFilesAPI_MetaCrossTenant404(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	owner := uuid.New()
	intruder := uuid.New()
	r := h.uploadBytes(t, h.mintToken(owner), "x", "text/plain", []byte("hi"), nil)
	defer r.body.Close()
	fileID := r.json["id"].(string)
	defer h.cleanupFile(owner, fileID)

	req, _ := http.NewRequest("GET",
		h.server.URL+"/v1/files/"+fileID+"/meta", nil)
	req.Header.Set("Authorization", "Bearer "+h.mintToken(intruder))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-tenant meta: expected 404, got %d", resp.StatusCode)
	}
}

// ─── Delete ───────────────────────────────────────────────

func TestFilesAPI_DeleteSoftIdempotent(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)

	r := h.uploadBytes(t, tok, "del.txt", "text/plain", []byte("delete me"), nil)
	defer r.body.Close()
	fileID := r.json["id"].(string)

	// First delete: 204
	req, _ := http.NewRequest("DELETE", h.server.URL+"/v1/files/"+fileID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete1: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("first delete: expected 204 got %d", resp.StatusCode)
	}

	// Second delete: 404
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete2: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("second delete: expected 404 got %d", resp2.StatusCode)
	}

	// GET after delete: 404
	greq, _ := http.NewRequest("GET", h.server.URL+"/v1/files/"+fileID, nil)
	greq.Header.Set("Authorization", "Bearer "+tok)
	gresp, err := http.DefaultClient.Do(greq)
	if err != nil {
		t.Fatalf("get after del: %v", err)
	}
	gresp.Body.Close()
	if gresp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete: expected 404 got %d", gresp.StatusCode)
	}
}

// ─── Download by sha (新端点 — sidebar 图标用) ──────────────

func TestFilesAPI_DownloadBySha_RoundTrip(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)

	payload := []byte("png-bytes-stand-in")
	r := h.uploadBytes(t, tok, "favicon.ico", "image/x-icon", payload, nil)
	defer r.body.Close()
	if r.status != 200 {
		t.Fatalf("upload: %d %s", r.status, r.bodyStr)
	}
	sha := r.json["sha256"].(string)
	defer h.cleanupFile(uid, r.json["id"].(string))

	req, _ := http.NewRequest("GET", h.server.URL+"/v1/brain/files-by-sha/"+sha, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("by-sha: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		bb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, bb)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/x-icon" {
		t.Errorf("Content-Type = %q, want image/x-icon", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, payload) {
		t.Errorf("payload mismatch")
	}
}

func TestFilesAPI_DownloadBySha_BadHex(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	tok := h.mintToken(uuid.New())
	for _, badSha := range []string{
		"shorty",
		"012345",
		strings.Repeat("z", 64),  // 64 但非 hex
	} {
		req, _ := http.NewRequest("GET", h.server.URL+"/v1/brain/files-by-sha/"+badSha, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("sha=%q: status %d, want 400", badSha, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestFilesAPI_DownloadBySha_NotFoundForOtherUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uidA := uuid.New()
	uidB := uuid.New()
	tokA := h.mintToken(uidA)
	tokB := h.mintToken(uidB)

	payload := []byte("user-a-secret-icon")
	r := h.uploadBytes(t, tokA, "f.bin", "application/octet-stream", payload, nil)
	defer r.body.Close()
	defer h.cleanupFile(uidA, r.json["id"].(string))
	sha := r.json["sha256"].(string)

	// User B 用同一 sha 拿不到 (sha256 是 user-scoped)。
	req, _ := http.NewRequest("GET", h.server.URL+"/v1/brain/files-by-sha/"+sha, nil)
	req.Header.Set("Authorization", "Bearer "+tokB)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("user B should get 404, got %d", resp.StatusCode)
	}
}

func TestFilesAPI_DownloadBySha_NoMatch404(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	tok := h.mintToken(uuid.New())
	// 64 hex 但不存在。
	sha := strings.Repeat("a", 64)
	req, _ := http.NewRequest("GET", h.server.URL+"/v1/brain/files-by-sha/"+sha, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ─── helpers ──────────────────────────────────────────────

type uploadResp struct {
	status  int
	json    map[string]any
	bodyStr string
	body    io.ReadCloser
}

func (h *apiHarness) uploadBytes(
	t *testing.T,
	token, filename, contentType string,
	payload []byte,
	metadata map[string]string,
) uploadResp {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{
		fmt.Sprintf(`form-data; name="file"; filename=%q`, filename),
	}
	hdr["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("multipart create: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	if metadata != nil {
		for k, v := range metadata {
			_ = mw.WriteField(k, v)
		}
	}
	mw.Close()

	req, _ := http.NewRequest("POST", h.server.URL+"/v1/files/upload", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	bb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	parsed := map[string]any{}
	if len(bb) > 0 && bb[0] == '{' {
		_ = json.Unmarshal(bb, &parsed)
	}
	return uploadResp{
		status:  resp.StatusCode,
		json:    parsed,
		bodyStr: string(bb),
		body:    io.NopCloser(bytes.NewReader(nil)),
	}
}

// MaxUploadBytes pin (跟 newAPIHarness 配置同步, 给 oversized test 用)。
func (h *apiHarness) maxUploadBytes() int64 { return 5 * 1024 * 1024 }

func (h *apiHarness) cleanupFile(uid uuid.UUID, fileID string) {
	id, err := uuid.Parse(fileID)
	if err != nil {
		return
	}
	_ = NewStore(h.pool).SoftDelete(context.Background(), uid, id)
}
