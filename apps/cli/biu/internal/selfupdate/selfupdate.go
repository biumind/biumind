// Package selfupdate implements in-place binary replacement for
// manually installed biu builds.
//
// Scope — ONLY for manual/unknown installs:
//
//   * homebrew installs delegate to `brew upgrade` (Cellar is owned
//     by brew; replacing the binary would desync it)
//   * `go install` builds delegate to `go install ...@<ver>` (GOPATH
//     semantics belong to go)
//   * desktop-client installs (~/.local/bin, version …+clientmanaged)
//     are skipped entirely — the client owns that plane
//
// The delegation decisions live in the caller (repl /upgrade run);
// this package is deliberately dumb: given an archive URL + its
// expected SHA-256, verify-then-swap the running executable.
//
// Safety contract:
//
//   1. SHA-256 verified BEFORE extraction — a tampered archive never
//      touches the filesystem beyond a temp file.
//   2. The new binary lands beside the current one via temp-file +
//      rename, so the swap is atomic; a crash mid-update leaves the
//      old binary working.
//   3. The previous binary survives as <exe>.old for manual rollback.
//   4. A single-flight lock (~/.biu/update.lock) stops two REPLs from
//      swapping concurrently; a stale lock older than 30min is stolen.
//   5. Every failure path cleans up its temp files.

package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadTimeout bounds the archive download (the binary is ~15 MB).
const downloadTimeout = 10 * time.Minute

// lockMaxAge is how old a stale update lock may be before it's stolen.
const lockMaxAge = 30 * time.Minute

// Options describe one self-update. AssetURL plus a SHA-256 source
// (AssetSha256 inline, or ChecksumsURL → GoReleaser checksums.txt)
// are the minimum viable set.
type Options struct {
	// AssetURL downloads the platform tar.gz.
	AssetURL string

	// AssetFilename is the archive's name, used to find its line in
	// checksums.txt.
	AssetFilename string

	// AssetSha256 is the expected hex digest. When empty, ChecksumsURL
	// is fetched and parsed for AssetFilename's line.
	AssetSha256 string

	// ChecksumsURL downloads checksums.txt (SHA-256 per asset).
	ChecksumsURL string

	// HTTPDo defaults to a redirect-following client with a 10-minute
	// timeout. Injection point for tests.
	HTTPDo func(*http.Request) (*http.Response, error)

	// ExePath defaults to os.Executable(). Tests point it at a temp
	// file.
	ExePath string

	// LockPath defaults to ~/.biu/update.lock.
	LockPath string
}

// Apply downloads, verifies, and swaps in the new binary. On success
// the caller must restart biu; the running process keeps the old
// image until exit.
func Apply(ctx context.Context, opt Options) error {
	if opt.AssetURL == "" {
		return errors.New("selfupdate: no asset url")
	}
	if opt.AssetSha256 == "" && opt.ChecksumsURL == "" {
		return errors.New("selfupdate: no sha256 source (need AssetSha256 or ChecksumsURL)")
	}

	do := opt.HTTPDo
	if do == nil {
		client := &http.Client{Timeout: downloadTimeout}
		do = client.Do
	}

	exe, err := exePath(opt.ExePath)
	if err != nil {
		return err
	}

	release, err := acquireLock(opt.LockPath)
	if err != nil {
		return err
	}
	defer release()

	want, err := resolveSha(ctx, opt, do)
	if err != nil {
		return err
	}

	// Download + hash to a temp archive.
	archive, err := os.CreateTemp("", "biu-update-*.tar.gz")
	if err != nil {
		return err
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)

	got, err := download(ctx, opt.AssetURL, do, archive)
	archive.Close()
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("selfupdate: sha256 mismatch for %s: got %s, want %s — refusing to install",
			opt.AssetFilename, got, want)
	}

	// Extract the biu entry into a temp file NEXT TO the current
	// binary (same filesystem → atomic rename).
	newBin, err := extractBinary(archivePath, filepath.Dir(exe))
	if err != nil {
		return err
	}
	defer os.Remove(newBin)

	// Preserve the current mode bits (manual installs are usually
	// 0755, but respect whatever the user set).
	if st, err := os.Stat(exe); err == nil {
		_ = os.Chmod(newBin, st.Mode().Perm())
	}

	// Swap: current → .old, new → current. If the final rename fails,
	// roll .old back so the binary keeps working.
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("selfupdate: backup current binary: %w", err)
	}
	if err := os.Rename(newBin, exe); err != nil {
		if rbErr := os.Rename(old, exe); rbErr != nil {
			return fmt.Errorf("selfupdate: install new binary: %v (rollback failed too: %v — recover %s manually)", err, rbErr, old)
		}
		return fmt.Errorf("selfupdate: install new binary: %w (old binary restored)", err)
	}
	return nil
}

// exePath resolves the target executable, following symlinks (a
// Homebrew-style layout or a user symlink must be swapped at its
// destination).
func exePath(override string) (string, error) {
	p := override
	if p == "" {
		var err error
		if p, err = os.Executable(); err != nil {
			return "", err
		}
	}
	return filepath.EvalSymlinks(p)
}

// resolveSha returns the expected hex SHA-256 for the asset.
func resolveSha(ctx context.Context, opt Options, do func(*http.Request) (*http.Response, error)) (string, error) {
	if opt.AssetSha256 != "" {
		return opt.AssetSha256, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opt.ChecksumsURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := do(req)
	if err != nil {
		return "", fmt.Errorf("selfupdate: fetch checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("selfupdate: fetch checksums: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	// GoReleaser format: "<sha>  <filename>" per line (two spaces).
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[1] == opt.AssetFilename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("selfupdate: %s not found in checksums.txt", opt.AssetFilename)
}

// download streams url into dst while hashing, returning the hex
// SHA-256.
func download(ctx context.Context, url string, do func(*http.Request) (*http.Response, error), dst io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "biu-selfupdate")
	resp, err := do(req)
	if err != nil {
		return "", fmt.Errorf("selfupdate: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("selfupdate: download: %s", resp.Status)
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, hasher), resp.Body); err != nil {
		return "", fmt.Errorf("selfupdate: download: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// extractBinary pulls the `biu` entry out of the tar.gz into a fresh
// temp file inside dir. Returns the temp path; the caller renames it
// over the live binary.
func extractBinary(archivePath, dir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("selfupdate: open archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", errors.New("selfupdate: no `biu` entry in archive")
		}
		if err != nil {
			return "", fmt.Errorf("selfupdate: read archive: %w", err)
		}
		// GoReleaser archives put the binary at the root; match by
		// base name so a future restructure (biu/bin/biu) still works.
		if !hdr.FileInfo().Mode().IsRegular() || filepath.Base(hdr.Name) != "biu" {
			continue
		}
		dst, err := os.CreateTemp(dir, ".biu-new-*")
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(dst, tr); err != nil {
			dst.Close()
			os.Remove(dst.Name())
			return "", fmt.Errorf("selfupdate: extract: %w", err)
		}
		if err := dst.Close(); err != nil {
			os.Remove(dst.Name())
			return "", err
		}
		return dst.Name(), nil
	}
}

// acquireLock takes the single-flight lock, returning a release func.
// A lock older than lockMaxAge is considered stale (crashed update)
// and stolen.
func acquireLock(override string) (func(), error) {
	path := override
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".biu", "update.lock")
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return nil, err
		}
		// Existing lock: steal it when stale.
		if st, statErr := os.Stat(path); statErr == nil && time.Since(st.ModTime()) > lockMaxAge {
			_ = os.Remove(path)
			f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		}
		if err != nil {
			return nil, fmt.Errorf("selfupdate: another update appears to be in progress (%s); retry later or rm it", path)
		}
	}
	_, _ = f.WriteString(fmt.Sprintf("pid=%d at=%s", os.Getpid(), time.Now().UTC().Format(time.RFC3339)))
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}
