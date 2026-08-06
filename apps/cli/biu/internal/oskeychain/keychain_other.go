// Stub for platforms without a keychain impl (anything not darwin/linux).
// Always unavailable → callers fall back to file storage. Windows DPAPI
// can land here later via a dedicated keychain_windows.go.

//go:build !darwin && !linux

package oskeychain

func openPlatform() (Keychain, bool) { return nil, false }
