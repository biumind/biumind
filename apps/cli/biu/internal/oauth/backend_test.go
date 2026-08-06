package oauth

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// stubKeychain is an in-memory tokenBackend used to assert Store
// behaves identically to a "real" platform keychain without going
// through the platform CLI.
type stubKeychain struct {
	mu      sync.Mutex
	tokens  Tokens
	present bool
}

func (s *stubKeychain) Name() string { return "stub-keychain" }
func (s *stubKeychain) Path() string { return "test://stub" }

func (s *stubKeychain) Load() (Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.present {
		return Tokens{}, nil
	}
	return s.tokens, nil
}

func (s *stubKeychain) Save(t Tokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = t
	s.present = true
	return nil
}

func (s *stubKeychain) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = Tokens{}
	s.present = false
	return nil
}

// TestStoreOpenPrefersKeychain — when the keychain test override
// returns a backend, Open() picks it over file.
func TestStoreOpenPrefersKeychain(t *testing.T) {
	stub := &stubKeychain{}
	restore := SetKeychainBackendForTest(func() (tokenBackend, bool) {
		return stub, true
	})
	defer restore()

	s, err := Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend() != "stub-keychain" {
		t.Errorf("Open should prefer keychain when available; backend=%q", s.Backend())
	}
}

// TestStoreOpenFallsBackToFile — keychain unavailable => Open returns
// file-backed store at the override path.
func TestStoreOpenFallsBackToFile(t *testing.T) {
	restore := SetKeychainBackendForTest(func() (tokenBackend, bool) {
		return nil, false
	})
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend() != "file" {
		t.Errorf("expected file backend; got %q", s.Backend())
	}
	if s.Path() != path {
		t.Errorf("path mismatch: got %q want %q", s.Path(), path)
	}
}

// TestMigrateMovesTokens — Migrate copies from src and clears src on
// success.
func TestMigrateMovesTokens(t *testing.T) {
	dir := t.TempDir()
	src, _ := NewStore(filepath.Join(dir, "from.json"))
	want := Tokens{
		AccessToken:  "at-migrate",
		RefreshToken: "rt-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Round(time.Second),
		Provider:     "test",
	}
	if err := src.Save(want); err != nil {
		t.Fatal(err)
	}

	stub := &stubKeychain{}
	dst := &Store{backend: stub}

	migrated, err := dst.Migrate(src)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("expected migrated=true with tokens present")
	}

	got, _ := dst.Load()
	if got.AccessToken != want.AccessToken {
		t.Errorf("dst missing tokens after migrate: %+v", got)
	}
	left, _ := src.Load()
	if left.AccessToken != "" {
		t.Errorf("src should be cleared after migrate; got %+v", left)
	}
}

// TestMigrateNoSourceTokens — when src has nothing, Migrate is a
// silent no-op.
func TestMigrateNoSourceTokens(t *testing.T) {
	dir := t.TempDir()
	src, _ := NewStore(filepath.Join(dir, "empty.json"))
	dst := &Store{backend: &stubKeychain{}}

	migrated, err := dst.Migrate(src)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Errorf("empty source should not report migrated=true")
	}
}

// TestMigrateNilSource is defensive — guards against future callers
// passing a nil store.
func TestMigrateNilSource(t *testing.T) {
	dst := &Store{backend: &stubKeychain{}}
	if _, err := dst.Migrate(nil); err == nil {
		t.Errorf("Migrate(nil) should error")
	}
}
