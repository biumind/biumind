// Persist permission updates back to settings.json files.
//
// biumind keeps the layered file paths explicit so each destination
// maps to a literal file on disk:
//
//   PermissionDestUserSettings    → ~/.biumind/settings.json
//   PermissionDestProjectSettings → <cwd>/.biumind/settings.json
//   PermissionDestLocalSettings   → <cwd>/.biumind/settings.local.json
//   PermissionDestSession         → not persisted
//   PermissionDestCliArg          → not persisted
//
// All writes are atomic (tmp + rename) so a crash mid-write leaves
// the original file intact. Read-modify-write is the only mode —
// callers never overwrite blindly. Concurrent callers are expected
// to be rare (single CLI session); we don't take a flock to keep
// this simple, but a follow-up can wrap with gofrs/flock if needed.

package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// SupportsPersistence reports whether dest is a destination
// PersistPermissionUpdate will actually write to disk for.
func SupportsPersistence(dest string) bool {
	switch dest {
	case sdkproto.PermissionDestUserSettings,
		sdkproto.PermissionDestProjectSettings,
		sdkproto.PermissionDestLocalSettings:
		return true
	}
	return false
}

// PersistPermissionUpdate routes a wire-format update to the
// settings file matching its destination. Session / CLI-arg
// destinations are no-ops (return nil) so callers can blindly call
// PersistPermissionUpdate after ApplyPermissionUpdate without
// branching.
//
// `cwd` is the project root used to resolve project / local
// destinations; pass "" if you only want user-layer writes (the
// project / local cases will return ErrCwdRequired).
func PersistPermissionUpdate(cwd string, u sdkproto.PermissionUpdate) error {
	if u == nil {
		return errors.New("settings: nil PermissionUpdate")
	}

	switch v := u.(type) {
	case *sdkproto.AddDirectories:
		return mutateLayer(cwd, v.Destination, func(s *Settings) {
			ensurePermissions(s)
			s.Permissions.AdditionalDirectories = appendUniqueStrings(
				s.Permissions.AdditionalDirectories, v.Directories)
		})

	case *sdkproto.RemoveDirectories:
		return mutateLayer(cwd, v.Destination, func(s *Settings) {
			if s.Permissions == nil {
				return
			}
			s.Permissions.AdditionalDirectories = removeStrings(
				s.Permissions.AdditionalDirectories, v.Directories)
		})

	case *sdkproto.AddRules:
		return mutateLayer(cwd, v.Destination, func(s *Settings) {
			ensurePermissions(s)
			rules := ruleStringsFromValues(v.Rules)
			s.Permissions.Allow = mergeForBehavior(
				s.Permissions.Allow, rules, v.Behavior == sdkproto.PermissionAllow)
			s.Permissions.Deny = mergeForBehavior(
				s.Permissions.Deny, rules, v.Behavior == sdkproto.PermissionDeny)
			s.Permissions.Ask = mergeForBehavior(
				s.Permissions.Ask, rules, v.Behavior == sdkproto.PermissionAsk)
		})

	case *sdkproto.ReplaceRules:
		return mutateLayer(cwd, v.Destination, func(s *Settings) {
			ensurePermissions(s)
			rules := ruleStringsFromValues(v.Rules)
			switch v.Behavior {
			case sdkproto.PermissionAllow:
				s.Permissions.Allow = rules
			case sdkproto.PermissionDeny:
				s.Permissions.Deny = rules
			case sdkproto.PermissionAsk:
				s.Permissions.Ask = rules
			}
		})

	case *sdkproto.RemoveRules:
		return mutateLayer(cwd, v.Destination, func(s *Settings) {
			if s.Permissions == nil {
				return
			}
			rules := ruleStringsFromValues(v.Rules)
			switch v.Behavior {
			case sdkproto.PermissionAllow:
				s.Permissions.Allow = removeStrings(s.Permissions.Allow, rules)
			case sdkproto.PermissionDeny:
				s.Permissions.Deny = removeStrings(s.Permissions.Deny, rules)
			case sdkproto.PermissionAsk:
				s.Permissions.Ask = removeStrings(s.Permissions.Ask, rules)
			}
		})

	case *sdkproto.SetModeUpdate:
		return mutateLayer(cwd, v.Destination, func(s *Settings) {
			ensurePermissions(s)
			s.Permissions.DefaultMode = v.Mode
		})
	}
	return fmt.Errorf("settings: unknown PermissionUpdate type %q",
		u.PermissionUpdateType())
}

// ErrCwdRequired is returned when a project/local destination is
// requested but no cwd was provided.
var ErrCwdRequired = errors.New("settings: cwd required for project/local destination")

// mutateLayer reads the file for destination, applies fn, and writes
// it back atomically. If the file doesn't exist, fn is called on a
// zero-value Settings and the result is written (creating the file
// + parent dir as needed).
func mutateLayer(cwd, dest string, fn func(*Settings)) error {
	path, err := pathForDestination(cwd, dest)
	if err != nil {
		return err
	}

	current, err := readFile(path)
	if err != nil {
		return fmt.Errorf("settings persist: %w", err)
	}
	if current == nil {
		current = &Settings{}
	}

	fn(current)

	return atomicWriteJSON(path, current)
}

// pathForDestination resolves dest to an absolute file path using
// the provided cwd. session / cliArg destinations return ErrSessionDest
// so callers can short-circuit (they should not have called us at
// all, but we want a clean error rather than a write to /dev/null).
func pathForDestination(cwd, dest string) (string, error) {
	switch dest {
	case sdkproto.PermissionDestUserSettings:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("settings: home dir: %w", err)
		}
		return filepath.Join(home, ".biumind", "settings.json"), nil
	case sdkproto.PermissionDestProjectSettings:
		if cwd == "" {
			return "", ErrCwdRequired
		}
		return filepath.Join(cwd, ".biumind", "settings.json"), nil
	case sdkproto.PermissionDestLocalSettings:
		if cwd == "" {
			return "", ErrCwdRequired
		}
		return filepath.Join(cwd, ".biumind", "settings.local.json"), nil
	case sdkproto.PermissionDestSession, sdkproto.PermissionDestCliArg:
		return "", ErrSessionDest
	}
	return "", fmt.Errorf("settings: unknown destination %q", dest)
}

// ErrSessionDest is returned when a non-persistable destination is
// passed to PersistPermissionUpdate. Caller should check
// SupportsPersistence first and skip the call when false.
var ErrSessionDest = errors.New("settings: destination is not persistable")

// atomicWriteJSON writes b to path via a temp file in the same
// directory + os.Rename so a crash mid-write leaves the original
// intact. Creates parent dirs as needed.
func atomicWriteJSON(path string, s *Settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("settings: mkdir %s: %w", dir, err)
	}

	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: marshal: %w", err)
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("settings: temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails before rename.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: fsync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("settings: close %s: %w", tmpName, err)
	}
	// 0o644 — settings.json is readable to keep `cat` workflows working.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("settings: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("settings: rename %s → %s: %w", tmpName, path, err)
	}
	return nil
}

// ensurePermissions makes sure s.Permissions is non-nil so callers
// can mutate it without branching.
func ensurePermissions(s *Settings) {
	if s.Permissions == nil {
		s.Permissions = &PermissionsBlock{}
	}
}

// appendUniqueStrings appends each item from extras that isn't
// already present in dst. Order: existing first, new appended in
// input order. Empty strings are skipped — they're never valid as
// directory paths or rule strings and would otherwise leak into the
// written JSON.
func appendUniqueStrings(dst, extras []string) []string {
	if len(extras) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst)+len(extras))
	for _, s := range dst {
		seen[s] = struct{}{}
	}
	for _, s := range extras {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		dst = append(dst, s)
	}
	return dst
}

// removeStrings filters out every string in toRemove from src,
// preserving the relative order of survivors.
func removeStrings(src, toRemove []string) []string {
	if len(src) == 0 || len(toRemove) == 0 {
		return src
	}
	drop := make(map[string]struct{}, len(toRemove))
	for _, s := range toRemove {
		drop[s] = struct{}{}
	}
	out := src[:0]
	for _, s := range src {
		if _, skip := drop[s]; skip {
			continue
		}
		out = append(out, s)
	}
	return out
}

// mergeForBehavior is a helper for AddRules persistence. When this
// rule's behaviour matches the current bucket, append; otherwise
// pass through.
func mergeForBehavior(current, rules []string, matches bool) []string {
	if !matches {
		return current
	}
	return appendUniqueStrings(current, rules)
}

// ruleStringsFromValues converts wire-format PermissionRuleValue
// entries to the on-disk string form expected by settings.json.
func ruleStringsFromValues(values []sdkproto.PermissionRuleValue) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v.RuleContent == "" {
			out = append(out, v.ToolName)
			continue
		}
		out = append(out, fmt.Sprintf("%s(%s)", v.ToolName, v.RuleContent))
	}
	return out
}
