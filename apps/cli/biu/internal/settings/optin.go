// Auto-mode opt-in marker — records that the user has seen and
// accepted the auto-permission-mode disclaimer.
//
// Security posture: only the USER layer (~/.biumind/settings.json) is
// ever read or written here. Project / local layers are deliberately
// ignored so a malicious repository cannot ship
// .biumind/settings.json with autoModeOptIn=true and silently skip
// the one-time confirmation (the marker gates unattended tool
// execution). ApplyToContext does not consume the field either — the
// opt-in check lives in the REPL's /mode handler.

package settings

import (
	"os"
	"path/filepath"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// AutoModeOptedIn reports whether the user-layer settings.json carries
// permissions.autoModeOptIn=true. Read errors and a missing file
// degrade to false (not opted in → the caller prompts).
func AutoModeOptedIn() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	s, err := readFile(filepath.Join(home, ".biumind", "settings.json"))
	if err != nil || s == nil || s.Permissions == nil {
		return false
	}
	return s.Permissions.AutoModeOptIn != nil && *s.Permissions.AutoModeOptIn
}

// SetAutoModeOptIn persists permissions.autoModeOptIn=true to the user
// layer via the same atomic read-modify-write path PermissionUpdates
// use.
func SetAutoModeOptIn() error {
	return mutateLayer("", sdkproto.PermissionDestUserSettings, func(s *Settings) {
		ensurePermissions(s)
		on := true
		s.Permissions.AutoModeOptIn = &on
	})
}
