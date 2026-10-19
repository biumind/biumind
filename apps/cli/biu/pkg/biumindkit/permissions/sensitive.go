// Sensitive-path matcher for the auto-mode guard. Pure string
// analysis on the request's path arguments — no filesystem access,
// so it works identically on every platform and in sandboxes.
//
// Three pattern families:
//
//   - basename secrets: .env, id_rsa, *.pem, … (credential material
//     that must not leak into model context or be overwritten)
//   - directory segments: .git/, .ssh/, .gnupg/ (control paths whose
//     contents subvert other safety layers)
//   - shell rc files: persistence vectors — a write that survives
//     the session and runs on every future shell

package permissions

import (
	"path/filepath"
	"strings"
)

// sensitiveBasenames matches the lowercase basename of the path.
// Glob syntax is filepath.Match (single component only).
var sensitiveBasenames = []string{
	".env",
	".env.*",
	"id_rsa",
	"id_ed25519",
	"id_ecdsa",
	"id_dsa",
	"*.pem",
	"*.key",
	"*.p12",
	"*.pfx",
	"*.keystore",
	"*.jks",
	"credentials", // ~/.aws/credentials, ~/.config/gcloud/.../credentials
	".netrc",
	".bashrc",
	".bash_profile",
	".zshrc",
	".zprofile",
	".profile",
}

// sensitiveSegments matches any lowercase path segment exactly.
var sensitiveSegments = []string{
	".git",
	".ssh",
	".gnupg",
	".aws",
}

// isSensitivePath reports whether p names a sensitive file or sits
// inside a sensitive directory. Case-insensitive; both slash styles
// accepted. Empty path returns false (no path semantic → not this
// guard's problem).
func isSensitivePath(p string) bool {
	if p == "" {
		return false
	}
	p = strings.ToLower(strings.ReplaceAll(filepath.ToSlash(p), `\`, "/"))
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	for _, pat := range sensitiveBasenames {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
	}
	for _, seg := range strings.Split(p, "/") {
		for _, s := range sensitiveSegments {
			if seg == s {
				return true
			}
		}
	}
	return false
}
