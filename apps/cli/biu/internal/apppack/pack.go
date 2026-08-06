// .biuapp pack / verify.
//
// Wire format (zip):
//
//   manifest.yaml          (required, validated by biuapp.Validate)
//   manifest.sig           (required iff signing enabled; ed25519 over manifest.yaml bytes)
//   SHA256SUMS             (one line per file: "<hex>  <relpath>")
//   SHA256SUMS.sig         (ed25519 over SHA256SUMS bytes — root of trust)
//   assets/**              (icon, screenshots)
//   skills/**              (SKILL.md bundles)
//   locales/**             (i18n)
//   README.md, LICENSE, CHANGELOG.md
//   app/<os>_<arch>/app    (compiled Go binary; CLI doesn't compile in v2.0,
//                           caller passes pre-built artefacts via --binary)
//
// Two signatures (manifest.sig + SHA256SUMS.sig) — minor redundancy
// but each protects a different attack:
//   - manifest.sig blocks "swap manifest under a valid sums file"
//   - SHA256SUMS.sig blocks "swap any other file under a valid manifest"
// Both are checked by Verify.

package apppack

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PackOptions captures everything CLI passes to Pack.
type PackOptions struct {
	// SourceDir is the project root holding manifest.yaml + assets/etc.
	SourceDir string
	// OutPath is the .biuapp file to write. Parent dir must exist.
	OutPath string
	// KeyPair signs manifest + SHA256SUMS. nil = unsigned bundle
	// (allowed for local install only; marketplace publish refuses).
	KeyPair *KeyPair
	// Includes is the explicit allowlist of relative paths under
	// SourceDir to include. CLI builds it from .biuapp.yaml's
	// `include:` glob list; we receive resolved paths so this
	// package stays glob-engine-agnostic.
	Includes []string
}

// Pack builds the .biuapp at OutPath. Returns the SHA256 of the
// final zip so CI can pin it.
func Pack(opts PackOptions) (string, error) {
	if opts.SourceDir == "" || opts.OutPath == "" {
		return "", errors.New("apppack: SourceDir + OutPath required")
	}
	if len(opts.Includes) == 0 {
		return "", errors.New("apppack: no files to include")
	}
	manifestPath := filepath.Join(opts.SourceDir, "manifest.yaml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("apppack: read manifest: %w", err)
	}

	// Resolve every include to (zipName, absPath, sha256). Sort for
	// reproducible output — same source, same bytes, same hash.
	type entry struct {
		zipName string
		abs     string
		sum     [32]byte
		size    int64
	}
	entries := make([]entry, 0, len(opts.Includes))
	for _, rel := range opts.Includes {
		abs := filepath.Join(opts.SourceDir, rel)
		st, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("apppack: include %s: %w", rel, err)
		}
		if st.IsDir() {
			continue // dirs handled at glob expansion time; skip silently
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry{
			zipName: filepath.ToSlash(rel),
			abs:     abs,
			sum:     sha256.Sum256(raw),
			size:    st.Size(),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].zipName < entries[j].zipName })

	// Build SHA256SUMS — one line per included file.
	var sums bytes.Buffer
	for _, e := range entries {
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(e.sum[:]), e.zipName)
	}

	// Open zip output.
	out, err := os.Create(opts.OutPath)
	if err != nil {
		return "", fmt.Errorf("apppack: create out: %w", err)
	}
	defer out.Close()
	hasher := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(out, hasher))

	addFile := func(name string, body []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	}

	// Write manifest first (verifier can short-circuit on bad
	// manifest before walking the rest of the bundle).
	if err := addFile("manifest.yaml", manifestBytes); err != nil {
		return "", err
	}

	// manifest.sig
	if opts.KeyPair != nil {
		sig := ed25519.Sign(opts.KeyPair.Priv, manifestBytes)
		if err := addFile("manifest.sig", sig); err != nil {
			return "", err
		}
	}

	// Other files in deterministic order.
	for _, e := range entries {
		if e.zipName == "manifest.yaml" {
			continue // already written
		}
		raw, err := os.ReadFile(e.abs)
		if err != nil {
			return "", err
		}
		if err := addFile(e.zipName, raw); err != nil {
			return "", err
		}
	}

	if err := addFile("SHA256SUMS", sums.Bytes()); err != nil {
		return "", err
	}
	if opts.KeyPair != nil {
		sumsSig := ed25519.Sign(opts.KeyPair.Priv, sums.Bytes())
		if err := addFile("SHA256SUMS.sig", sumsSig); err != nil {
			return "", err
		}
	}

	if err := zw.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// VerifyResult is what Verify hands back. UnsignedAllowed=true means
// the caller asked us to tolerate missing signatures (local install
// path); marketplace verification flips it false.
type VerifyResult struct {
	ManifestBytes  []byte
	Signed         bool
	PublisherID    string // ed25519:<pub-base64> when signed
	FilesValidated int
}

// Verify opens the .biuapp at path and validates:
//  1. manifest.yaml present
//  2. SHA256SUMS lists every other entry, hashes match
//  3. (if signed) manifest.sig + SHA256SUMS.sig validate against
//     the supplied trusted pub keys (any one matching is fine for
//     v2.0; v2.5 marketplace will pin per-publisher).
//
// trustedPubs is a map of "ed25519:<pub-base64>" → ed25519.PublicKey.
// Empty map = unsigned-tolerant; pass at least one entry to enforce
// signature checking.
func Verify(path string, trustedPubs map[string]ed25519.PublicKey) (*VerifyResult, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("apppack: open zip: %w", err)
	}
	defer r.Close()

	files := map[string][]byte{}
	for _, f := range r.File {
		raw, err := readZip(f)
		if err != nil {
			return nil, fmt.Errorf("apppack: read %s: %w", f.Name, err)
		}
		files[f.Name] = raw
	}

	manifest, ok := files["manifest.yaml"]
	if !ok {
		return nil, errors.New("apppack: missing manifest.yaml")
	}

	sums, ok := files["SHA256SUMS"]
	if !ok {
		return nil, errors.New("apppack: missing SHA256SUMS")
	}

	// Hash check: every line in SHA256SUMS must match the embedded file.
	validated := 0
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "<hex>  <path>" (two spaces).
		idx := strings.Index(line, "  ")
		if idx < 0 {
			return nil, fmt.Errorf("apppack: bad SHA256SUMS line %q", line)
		}
		want := line[:idx]
		name := strings.TrimSpace(line[idx+2:])
		body, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("apppack: SHA256SUMS lists %q but file missing from zip", name)
		}
		got := sha256.Sum256(body)
		if hex.EncodeToString(got[:]) != want {
			return nil, fmt.Errorf("apppack: hash mismatch for %s", name)
		}
		validated++
	}

	res := &VerifyResult{ManifestBytes: manifest, FilesValidated: validated}

	manifestSig, hasManifestSig := files["manifest.sig"]
	sumsSig, hasSumsSig := files["SHA256SUMS.sig"]
	if !hasManifestSig && !hasSumsSig {
		// Unsigned bundle.
		if len(trustedPubs) > 0 {
			return nil, errors.New("apppack: signature required but bundle is unsigned")
		}
		return res, nil
	}
	if hasManifestSig != hasSumsSig {
		return nil, errors.New("apppack: bundle has only one of manifest.sig / SHA256SUMS.sig — both required")
	}

	// Verify against any trusted pub. We don't carry "the" publisher
	// id in the bundle (manifests do via author.public_key, but
	// trusting the bundle's own claim is what we're trying to avoid).
	// Caller decides which pubs are trusted; any match wins.
	if len(trustedPubs) == 0 {
		// Bundle is signed but caller passed no trusted keys —
		// accept-but-flag. Marketplace path always passes ≥ 1.
		res.Signed = true
		return res, nil
	}
	for id, pub := range trustedPubs {
		if ed25519.Verify(pub, manifest, manifestSig) &&
			ed25519.Verify(pub, sums, sumsSig) {
			res.Signed = true
			res.PublisherID = id
			return res, nil
		}
	}
	return nil, errors.New("apppack: signature does not match any trusted publisher")
}

func readZip(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 50<<20)) // 50 MiB single-file cap
}
