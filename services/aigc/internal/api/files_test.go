package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/aigc/internal/blob"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
)

// fakeBlob — 注入到 Server.Blob, 不起真 MinIO.
type fakeBlob struct {
	data string
	err  error
	// 记录最后一次请求的 bucket/key 供断言.
	gotBucket string
	gotKey    string
}

func (f *fakeBlob) Get(_ context.Context, bucket, key string) (io.ReadCloser, *blob.ObjectInfo, error) {
	f.gotBucket, f.gotKey = bucket, key
	if f.err != nil {
		return nil, nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.data)),
		&blob.ObjectInfo{Size: int64(len(f.data)), ContentType: "image/png"}, nil
}

const sha64 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDownloadBySha_BadShaLen(t *testing.T) {
	srv, mux := newTestServer(t)
	srv.Blob = &fakeBlob{data: "x"}
	uid := uuid.New()
	req := httptest.NewRequest("GET", "/v1/files-by-sha/tooshort", nil)
	req.Header.Set("Authorization", "Bearer "+issueToken(t, uid, "free", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d want 400 (body=%s)", w.Code, w.Body.String())
	}
}

func TestDownloadBySha_BlobNotWired(t *testing.T) {
	srv, mux := newTestServer(t)
	srv.Blob = nil // 显式无 blob
	uid := uuid.New()
	req := httptest.NewRequest("GET", "/v1/files-by-sha/"+sha64, nil)
	req.Header.Set("Authorization", "Bearer "+issueToken(t, uid, "free", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d want 503", w.Code)
	}
}

func TestDownloadBySha_NotFound(t *testing.T) {
	srv, mux := newTestServer(t)
	srv.Blob = &fakeBlob{data: "x"}
	uid := uuid.New()
	// 一个不存在的 sha → lookup ErrNotFound → 404
	req := httptest.NewRequest("GET", "/v1/files-by-sha/"+sha64, nil)
	req.Header.Set("Authorization", "Bearer "+issueToken(t, uid, "free", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d want 404 (body=%s)", w.Code, w.Body.String())
	}
}

func TestDownloadBySha_Forbidden_OthersPrivate(t *testing.T) {
	srv, mux := newTestServer(t)
	fb := &fakeBlob{data: "PNGDATA"}
	srv.Blob = fb
	ctx := context.Background()
	owner := uuid.New()
	// owner 写一张私有图
	sha := ("ab" + uuid.NewString() + uuid.NewString())[:64]
	seedTaskOutputForAPI(t, srv, owner, false, sha)

	// 另一个用户来拉 → 403
	other := uuid.New()
	req := httptest.NewRequest("GET", "/v1/files-by-sha/"+sha, nil)
	req.Header.Set("Authorization", "Bearer "+issueToken(t, other, "free", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d want 403 (body=%s)", w.Code, w.Body.String())
	}
	_ = ctx
}

func TestDownloadBySha_OK_OwnPrivate(t *testing.T) {
	srv, mux := newTestServer(t)
	fb := &fakeBlob{data: "PNGBYTES"}
	srv.Blob = fb
	owner := uuid.New()
	sha := ("cd" + uuid.NewString() + uuid.NewString())[:64]
	key := store.CASKey("outputs", sha, "png")
	seedTaskOutputForAPI(t, srv, owner, false, sha)

	req := httptest.NewRequest("GET", "/v1/files-by-sha/"+sha, nil)
	req.Header.Set("Authorization", "Bearer "+issueToken(t, owner, "free", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d want 200 (body=%s)", w.Code, w.Body.String())
	}
	if w.Body.String() != "PNGBYTES" {
		t.Errorf("body = %q", w.Body.String())
	}
	if fb.gotBucket != "outputs" || fb.gotKey != key {
		t.Errorf("blob fetched %q/%q want outputs/%q", fb.gotBucket, fb.gotKey, key)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q (want immutable)", cc)
	}
}

func TestDownloadBySha_OK_PublicCrossUser(t *testing.T) {
	srv, mux := newTestServer(t)
	srv.Blob = &fakeBlob{data: "PUB"}
	owner := uuid.New()
	sha := ("ef" + uuid.NewString() + uuid.NewString())[:64]
	seedTaskOutputForAPI(t, srv, owner, true, sha) // public

	other := uuid.New()
	req := httptest.NewRequest("GET", "/v1/files-by-sha/"+sha, nil)
	req.Header.Set("Authorization", "Bearer "+issueToken(t, other, "free", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("public asset cross-user = %d want 200 (body=%s)", w.Code, w.Body.String())
	}
}

func TestDownloadBySha_ObjectMissing(t *testing.T) {
	srv, mux := newTestServer(t)
	srv.Blob = &fakeBlob{err: blob.ErrNotFound} // DB 有记录但桶里没对象
	owner := uuid.New()
	sha := ("12" + uuid.NewString() + uuid.NewString())[:64]
	seedTaskOutputForAPI(t, srv, owner, false, sha)

	req := httptest.NewRequest("GET", "/v1/files-by-sha/"+sha, nil)
	req.Header.Set("Authorization", "Bearer "+issueToken(t, owner, "free", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d want 404 (object missing)", w.Code)
	}
}

// seedTaskOutputForAPI 写 task + output (复用 store), 供 api 层测试.
func seedTaskOutputForAPI(t *testing.T, s *Server, uid uuid.UUID, public bool, sha string) {
	t.Helper()
	ctx := context.Background()
	ensureSeedTestModel(t, s)
	tk, err := s.Store.CreateTask(ctx, store.CreateTaskArgs{
		UserID: uid, Type: "image",
		ModelCode: "test-img-model", ProviderCode: "test-prov",
		Prompt: "files test", CostCredits: 10, IsPublic: public,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := s.Store.CreateTaskOutput(ctx, store.CreateTaskOutputArgs{
		TaskID: tk.ID, Idx: 0, Kind: "image", SHA256: sha,
		StorageURL: "cas:" + sha, StorageKey: store.CASKey("outputs", sha, "png"),
		MimeType: "image/png",
	}); err != nil {
		t.Fatalf("create output: %v", err)
	}
}
