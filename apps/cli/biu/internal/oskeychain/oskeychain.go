// Package oskeychain — generic OS credential store accessed via the
// platform-native CLI (`security` on macOS, `secret-tool` on Linux).
//
// This is the LOW-LEVEL primitive: arbitrary (service, account) → string
// value. Two consumers layer on top:
//   - internal/oauth — stores the OAuth Tokens JSON under account "default".
//   - internal/secretstore — stores agent-plane secrets (device token,
//     X25519 privkey) under their own accounts, with a file fallback.
//
// Why shell out instead of CGO bindings (same rationale as the original
// oauth keychain backends this was extracted from):
//   - keeps CGO_ENABLED=0 for the static binary
//   - the platform CLI ships with the OS / desktop that has a keychain
//     to begin with — if it's missing we don't have a keychain anyway.
//
// Each GOOS build file provides openPlatform(), returning (nil, false)
// when the host keychain isn't reachable. Open() honours a test override
// before delegating.
package oskeychain

// Keychain is the generic credential primitive. Implementations MUST be
// safe for serial use (callers serialise if needed).
type Keychain interface {
	// Get returns the stored value for (service, account). found=false
	// (with nil error) means "no such entry" — distinct from a real error.
	Get(service, account string) (value string, found bool, err error)
	Set(service, account, value string) error
	Delete(service, account string) error
	// Name is a stable backend identifier (e.g. "darwin-keychain",
	// "secret-service") surfaced by `biu doctor`.
	Name() string
}

// testOverride lets tests inject a fake keychain without the platform
// CLI. nil + set=true simulates "no keychain available".
var (
	testOverride    Keychain
	testOverrideSet bool
)

// SetForTest swaps in a stub (or nil to simulate unavailability). Returns
// a restore function the test must defer-call. Not concurrency-safe.
func SetForTest(k Keychain) func() {
	prevK, prevSet := testOverride, testOverrideSet
	testOverride, testOverrideSet = k, true
	return func() { testOverride, testOverrideSet = prevK, prevSet }
}

// Open returns the platform keychain and true, or (nil, false) when no
// OS keychain is reachable (headless server, minimal container, unknown
// GOOS) — callers fall back to a file store.
func Open() (Keychain, bool) {
	if testOverrideSet {
		return testOverride, testOverride != nil
	}
	return openPlatform()
}
