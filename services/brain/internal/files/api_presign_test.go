// Tests for the two-step (presign-upload + finalize) attachment flow.
// Uses the same harness as api_test.go (real Postgres + real MinIO,
// skipped when env not wired). The PUT to MinIO goes through net/http
// directly — that's the exact path Flutter / browser clients take.

package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type presignedURL struct {
	FileID    string `json:"file_id"`
	UploadURL string `json:"upload_url"`
	Headers   map[string]string
	ObjectKey string `json:"object_key"`
}

// presignUpload — 调 /v1/files/presign-upload, 返回签名 URL 信息。
// 测试拼出 expected status 时调用方自检, 不在 helper 里 fail。
func (h *apiHarness) presignUpload(t *testing.T, token, filename, mime string, size int64, source string) (status int, body presignedURL, raw string) {
	t.Helper()
	in := map[string]any{
		"filename": filename, "mime": mime, "size": size, "source": source,
	}
	bs, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/files/presign-upload", bytes.NewReader(bs))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presign req: %v", err)
	}
	defer resp.Body.Close()
	bb, _ := io.ReadAll(resp.Body)
	out := presignedURL{}
	if len(bb) > 0 && bb[0] == '{' {
		// headers are nested, decode in two steps to keep the struct flat.
		var raw2 struct {
			FileID    string            `json:"file_id"`
			UploadURL string            `json:"upload_url"`
			Headers   map[string]string `json:"headers"`
			ObjectKey string            `json:"object_key"`
		}
		_ = json.Unmarshal(bb, &raw2)
		out = presignedURL(raw2)
	}
	return resp.StatusCode, out, string(bb)
}

func (h *apiHarness) putToMinio(t *testing.T, signed presignedURL, payload []byte) int {
	t.Helper()
	req, _ := http.NewRequest("PUT", signed.UploadURL, bytes.NewReader(payload))
	for k, v := range signed.Headers {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MinIO PUT: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (h *apiHarness) finalize(t *testing.T, token, fileID string, sha256Hex string, size int64) (int, map[string]any, string) {
	t.Helper()
	in := map[string]any{"file_id": fileID, "sha256": sha256Hex, "size": size}
	bs, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", h.server.URL+"/v1/files/finalize", bytes.NewReader(bs))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	defer resp.Body.Close()
	bb, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(bb) > 0 && bb[0] == '{' {
		_ = json.Unmarshal(bb, &out)
	}
	return resp.StatusCode, out, string(bb)
}

func sha256Hex(p []byte) string {
	h := sha256.Sum256(p)
	return hex.EncodeToString(h[:])
}

// ─── Happy path ────────────────────────────────────────────

func TestPresignUploadFinalizeHappyPath(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)

	payload := []byte("fake-png-bytes-" + uuid.New().String())
	status, signed, raw := h.presignUpload(t, tok, "x.png", "image/png", int64(len(payload)), "chat-attachment")
	if status != 200 {
		t.Fatalf("presign: %d %s", status, raw)
	}
	if signed.FileID == "" || signed.UploadURL == "" {
		t.Fatalf("presign missing fields: %+v", signed)
	}
	defer h.cleanupFile(uid, signed.FileID)

	if code := h.putToMinio(t, signed, payload); code >= 400 {
		t.Fatalf("MinIO PUT: %d", code)
	}

	st, body, raw := h.finalize(t, tok, signed.FileID, sha256Hex(payload), int64(len(payload)))
	if st != 200 {
		t.Fatalf("finalize: %d %s", st, raw)
	}
	if body["deduped"] != false {
		t.Errorf("first finalize deduped flag wrong: %+v", body)
	}
	if body["file_id"] != signed.FileID {
		t.Errorf("finalize returned different id: %v", body["file_id"])
	}

	// Download verifies bytes are real and tied to this user.
	greq, _ := http.NewRequest("GET", h.server.URL+"/v1/files/"+signed.FileID, nil)
	greq.Header.Set("Authorization", "Bearer "+tok)
	gresp, err := http.DefaultClient.Do(greq)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer gresp.Body.Close()
	if gresp.StatusCode != 200 {
		t.Fatalf("download: %d", gresp.StatusCode)
	}
	got, _ := io.ReadAll(gresp.Body)
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch")
	}
}

// ─── Dedup: same user, same content → same file_id ──────────

func TestPresignFinalizeDedupSameUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)
	payload := []byte("dedup-presign-" + uuid.New().String())
	sha := sha256Hex(payload)
	size := int64(len(payload))

	// First: full round-trip via multipart upload (gives us a 'ready' object
	// to dedup against — easier than two presign cycles in this test).
	r1 := h.uploadBytes(t, tok, "a.png", "image/png", payload, nil)
	defer r1.body.Close()
	if r1.status != 200 {
		t.Fatalf("first multipart upload: %d %s", r1.status, r1.bodyStr)
	}
	id1 := r1.json["id"].(string)
	defer h.cleanupFile(uid, id1)

	// Second: presign + put + finalize → expect deduped to existing id.
	_, signed, _ := h.presignUpload(t, tok, "b.png", "image/png", size, "chat-attachment")
	if code := h.putToMinio(t, signed, payload); code >= 400 {
		t.Fatalf("MinIO PUT: %d", code)
	}
	st, body, raw := h.finalize(t, tok, signed.FileID, sha, size)
	if st != 200 {
		t.Fatalf("finalize: %d %s", st, raw)
	}
	if body["deduped"] != true {
		t.Errorf("expected deduped=true, got %+v", body)
	}
	if body["file_id"] != id1 {
		t.Errorf("dedup should return original id %s, got %v", id1, body["file_id"])
	}
}

// ─── Validation errors ─────────────────────────────────────

func TestPresignRejectsDisallowedMime(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	st, _, raw := h.presignUpload(t, h.mintToken(uuid.New()),
		"x.exe", "application/x-msdownload", 100, "chat-attachment")
	if st != 400 {
		t.Errorf("expected 400 for disallowed mime, got %d %s", st, raw)
	}
	if !strings.Contains(raw, "mime_not_allowed") {
		t.Errorf("error code missing: %s", raw)
	}
}

func TestPresignRejectsOverMaxSize(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	st, _, raw := h.presignUpload(t, h.mintToken(uuid.New()),
		"big.png", "image/png", h.maxUploadBytes()+1, "chat-attachment")
	if st != 413 {
		t.Errorf("expected 413, got %d %s", st, raw)
	}
}

func TestFinalizeRejectsBlobMissing(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)
	payload := []byte("bytes-not-uploaded")
	_, signed, _ := h.presignUpload(t, tok, "x.png", "image/png", int64(len(payload)), "chat-attachment")
	defer h.cleanupFile(uid, signed.FileID)

	// Skip the PUT step → finalize should reject with blob_missing.
	st, _, raw := h.finalize(t, tok, signed.FileID, sha256Hex(payload), int64(len(payload)))
	if st != 400 {
		t.Errorf("expected 400 blob_missing, got %d %s", st, raw)
	}
	if !strings.Contains(raw, "blob_missing") {
		t.Errorf("error code missing: %s", raw)
	}
}

func TestFinalizeRejectsSizeMismatch(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)
	payload := []byte("real-bytes")
	_, signed, _ := h.presignUpload(t, tok, "x.png", "image/png", 999, "chat-attachment")
	defer h.cleanupFile(uid, signed.FileID)
	if code := h.putToMinio(t, signed, payload); code >= 400 {
		t.Fatalf("MinIO PUT: %d", code)
	}
	// claim wrong size in finalize → reject
	st, _, raw := h.finalize(t, tok, signed.FileID, sha256Hex(payload), 999)
	if st != 400 {
		t.Errorf("expected 400 size_mismatch, got %d %s", st, raw)
	}
	if !strings.Contains(raw, "size_mismatch") {
		t.Errorf("error code missing: %s", raw)
	}
}

func TestFinalizeRejectsCrossUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uidA := uuid.New()
	uidB := uuid.New()
	payload := []byte("a-payload-" + uuid.New().String())

	_, signed, _ := h.presignUpload(t, h.mintToken(uidA),
		"x.png", "image/png", int64(len(payload)), "chat-attachment")
	defer h.cleanupFile(uidA, signed.FileID)
	if code := h.putToMinio(t, signed, payload); code >= 400 {
		t.Fatalf("MinIO PUT: %d", code)
	}

	// uidB tries to finalize uidA's pending → 404 (don't 403, prevents enumeration)
	st, _, raw := h.finalize(t, h.mintToken(uidB), signed.FileID, sha256Hex(payload), int64(len(payload)))
	if st != 404 {
		t.Errorf("cross-user finalize: expected 404 got %d %s", st, raw)
	}
}

func TestFinalizeRejectsBadSha256(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)
	payload := []byte("payload")
	_, signed, _ := h.presignUpload(t, tok, "x.png", "image/png", int64(len(payload)), "chat-attachment")
	defer h.cleanupFile(uid, signed.FileID)
	if code := h.putToMinio(t, signed, payload); code >= 400 {
		t.Fatalf("MinIO PUT: %d", code)
	}
	st, _, raw := h.finalize(t, tok, signed.FileID, "not-hex-not-64-chars", int64(len(payload)))
	if st != 400 {
		t.Errorf("expected 400 bad_sha256, got %d %s", st, raw)
	}
	if !strings.Contains(raw, "bad_sha256") {
		t.Errorf("error code missing: %s", raw)
	}
}

// ─── presign-get: model-relay adapter path ─────────────────────────

// presignGet calls /v1/files/{id}/presign-get and returns the response.
func (h *apiHarness) presignGet(t *testing.T, token, fileID string) (int, map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("%s/v1/files/%s/presign-get", h.server.URL, fileID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presign-get: %v", err)
	}
	defer resp.Body.Close()
	bb, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(bb) > 0 && bb[0] == '{' {
		_ = json.Unmarshal(bb, &out)
	}
	return resp.StatusCode, out, string(bb)
}

func TestPresignGetHappyPath(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)

	// 用 multipart 路径快速建一个 ready 对象 (避开 presign-upload 流程)
	payload := []byte("presign-get-payload-" + uuid.New().String())
	r := h.uploadBytes(t, tok, "x.png", "image/png", payload, nil)
	defer r.body.Close()
	if r.status != 200 {
		t.Fatalf("upload: %d %s", r.status, r.bodyStr)
	}
	fileID := r.json["id"].(string)
	defer h.cleanupFile(uid, fileID)

	st, body, raw := h.presignGet(t, tok, fileID)
	if st != 200 {
		t.Fatalf("presign-get: %d %s", st, raw)
	}
	signedURL, _ := body["url"].(string)
	if signedURL == "" {
		t.Fatalf("missing url in response: %s", raw)
	}
	if mt, _ := body["media_type"].(string); mt != "image/png" {
		t.Errorf("media_type: got %v want image/png", body["media_type"])
	}

	// Anyone holding the URL within TTL can fetch — confirms it's a valid
	// presigned download URL pointing to MinIO directly.
	resp, err := http.Get(signedURL)
	if err != nil {
		t.Fatalf("GET signed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		bb, _ := io.ReadAll(resp.Body)
		t.Fatalf("signed GET status=%d body=%s", resp.StatusCode, bb)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch")
	}
}

func TestPresignGetRejectsCrossUser(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uidA := uuid.New()
	uidB := uuid.New()
	r := h.uploadBytes(t, h.mintToken(uidA),
		"x.png", "image/png", []byte("xross-presign-"+uuid.New().String()), nil)
	defer r.body.Close()
	fileID := r.json["id"].(string)
	defer h.cleanupFile(uidA, fileID)

	st, _, raw := h.presignGet(t, h.mintToken(uidB), fileID)
	if st != 404 {
		t.Errorf("cross-user presign-get: expected 404 got %d %s", st, raw)
	}
}

func TestPresignGetRejectsPending(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)
	// presign-upload 但不 finalize → status='pending' → presign-get 应 404。
	_, signed, _ := h.presignUpload(t, tok, "x.png", "image/png", 10, "chat-attachment")
	defer h.cleanupFile(uid, signed.FileID)
	st, _, raw := h.presignGet(t, tok, signed.FileID)
	if st != 404 {
		t.Errorf("pending presign-get: expected 404 got %d %s", st, raw)
	}
}

func TestPresignGetRejectsBadID(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	st, _, _ := h.presignGet(t, h.mintToken(uuid.New()), "not-a-uuid")
	if st != 400 {
		t.Errorf("bad id: expected 400 got %d", st)
	}
}

// ─── Status: pending row not visible via Get/Meta until finalize ───

func TestPendingRowNotVisibleBeforeFinalize(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	uid := uuid.New()
	tok := h.mintToken(uid)

	_, signed, _ := h.presignUpload(t, tok, "x.png", "image/png", 10, "chat-attachment")
	defer h.cleanupFile(uid, signed.FileID)

	// /meta should 404 — row exists but status='pending', Get filters it.
	req, _ := http.NewRequest("GET",
		fmt.Sprintf("%s/v1/files/%s/meta", h.server.URL, signed.FileID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("pending /meta: expected 404 got %d", resp.StatusCode)
	}

	// Verify the row is actually in DB as pending (paranoia — confirms the
	// implementation isn't faking it).
	store := NewStore(h.pool)
	if _, err := store.GetPending(context.Background(), uid, uuid.MustParse(signed.FileID)); err != nil {
		t.Errorf("pending row should exist in DB, GetPending: %v", err)
	}
}
