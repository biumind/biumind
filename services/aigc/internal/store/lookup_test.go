package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCASKey(t *testing.T) {
	sha := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if got := CASKey("derivatives", sha, "jpg"); got != "derivatives/ab/cd/"+sha+".jpg" {
		t.Errorf("CASKey derivatives = %q", got)
	}
	if got := CASKey("outputs", sha, "webp"); got != "outputs/ab/cd/"+sha+".webp" {
		t.Errorf("CASKey outputs = %q", got)
	}
	// 短 sha fallback (不该 panic)
	if got := CASKey("outputs", "ab", "png"); got != "outputs/ab.png" {
		t.Errorf("CASKey short = %q", got)
	}
}

// seedOutputTask 写一个 completed task + 1 output, 返回 (taskID, sha).
func seedOutputTask(t *testing.T, s *Store, uid uuid.UUID, public bool, args CreateTaskOutputArgs) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	prov, model := ensureSeedProviderModel(t, s)
	tk, err := s.CreateTask(ctx, CreateTaskArgs{
		UserID: uid, Type: "image", ModelCode: model, ProviderCode: prov,
		Prompt: "lookup test", CostCredits: 10, IsPublic: public,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	args.TaskID = tk.ID
	if _, err := s.CreateTaskOutput(ctx, args); err != nil {
		t.Fatalf("create output: %v", err)
	}
	return tk.ID
}

func TestLookupOutputBySha_OutputBody(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	sha := "11" + uuid.NewString()[:30] + uuid.NewString()[:32] // 64 chars-ish unique
	sha = sha[:64]
	key := CASKey("outputs", sha, "png")
	seedOutputTask(t, s, uid, false, CreateTaskOutputArgs{
		Idx: 0, Kind: "image", SHA256: sha,
		StorageURL: "cas:" + sha, StorageKey: key,
		MimeType: "image/png", Width: 1024, Height: 1024,
	})

	loc, err := s.LookupOutputBySha(context.Background(), sha)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if loc.Bucket != "outputs" || loc.StorageKey != key {
		t.Errorf("bucket/key = %q/%q", loc.Bucket, loc.StorageKey)
	}
	if loc.OwnerUserID != uid || loc.IsPublic {
		t.Errorf("owner/public = %v/%v", loc.OwnerUserID, loc.IsPublic)
	}
	if loc.MimeType != "image/png" {
		t.Errorf("mime = %q", loc.MimeType)
	}
}

func TestLookupOutputBySha_CoverDerivative(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	bodySha := ("22" + uuid.NewString() + uuid.NewString())[:64]
	coverSha := ("33" + uuid.NewString() + uuid.NewString())[:64]
	seedOutputTask(t, s, uid, true, CreateTaskOutputArgs{
		Idx: 0, Kind: "video", SHA256: bodySha,
		StorageURL: "cas:" + bodySha, StorageKey: CASKey("outputs", bodySha, "mp4"),
		CoverSHA: coverSha, MimeType: "video/mp4",
	})

	// cover sha 命中 → derivatives 桶, key 按 CAS 推导, mime jpeg
	loc, err := s.LookupOutputBySha(context.Background(), coverSha)
	if err != nil {
		t.Fatalf("lookup cover: %v", err)
	}
	if loc.Bucket != "derivatives" {
		t.Errorf("bucket = %q want derivatives", loc.Bucket)
	}
	if loc.StorageKey != CASKey("derivatives", coverSha, "jpg") {
		t.Errorf("cover key = %q", loc.StorageKey)
	}
	if loc.MimeType != "image/jpeg" {
		t.Errorf("cover mime = %q", loc.MimeType)
	}
	if !loc.IsPublic {
		t.Errorf("cover should inherit task is_public=true")
	}
}

func TestLookupOutputBySha_MetadataDerivative(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	bodySha := ("44" + uuid.NewString() + uuid.NewString())[:64]
	derivSha := ("55" + uuid.NewString() + uuid.NewString())[:64]
	seedOutputTask(t, s, uid, false, CreateTaskOutputArgs{
		Idx: 0, Kind: "image", SHA256: bodySha,
		StorageURL: "cas:" + bodySha, StorageKey: CASKey("outputs", bodySha, "png"),
		MimeType: "image/png",
		Metadata: map[string]any{
			"derivatives": []map[string]any{
				{"w": 480, "h": 480, "sha": derivSha, "mime": "image/webp"},
			},
		},
	})

	loc, err := s.LookupOutputBySha(context.Background(), derivSha)
	if err != nil {
		t.Fatalf("lookup derivative: %v", err)
	}
	if loc.Bucket != "derivatives" {
		t.Errorf("bucket = %q", loc.Bucket)
	}
	if loc.StorageKey != CASKey("derivatives", derivSha, "webp") {
		t.Errorf("deriv key = %q", loc.StorageKey)
	}
	if loc.MimeType != "image/webp" {
		t.Errorf("deriv mime = %q", loc.MimeType)
	}
}

func TestLookupOutputBySha_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.LookupOutputBySha(context.Background(),
		"deadbeef00000000000000000000000000000000000000000000000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v want ErrNotFound", err)
	}
}

func TestLookupOutputBySha_SoftDeletedHidden(t *testing.T) {
	s := newTestStore(t)
	uid := newUser()
	resetUser(t, s, uid)
	sha := ("66" + uuid.NewString() + uuid.NewString())[:64]
	tkID := seedOutputTask(t, s, uid, false, CreateTaskOutputArgs{
		Idx: 0, Kind: "image", SHA256: sha,
		StorageURL: "cas:" + sha, StorageKey: CASKey("outputs", sha, "png"),
		MimeType: "image/png",
	})
	if _, err := s.SoftDeleteTasks(context.Background(), uid, []uuid.UUID{tkID}); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	_, err := s.LookupOutputBySha(context.Background(), sha)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("软删后应 ErrNotFound, got %v", err)
	}
}
