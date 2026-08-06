// /add-dir + /remove-dir slash commands.
//
// Registers an additional working directory the model may read/write
// in. biu picks the args-only flavour (no interactive form) so the
// slash handler stays a single screen of Go while preserving the same
// five validation states reported to the user.
//
// Two destinations:
//
//   /add-dir <path>            → session (this REPL only)
//   /add-dir <path> --remember → localSettings (writes to
//                                .biumind/settings.local.json)
//
// /remove-dir is the symmetric operation. Removing from session is
// always safe; removing a path sourced from settings.local.json
// also rewrites the file (and warns if the path was sourced from a
// different settings layer the slash can't safely edit).

package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// handleAddDir parses `/add-dir <path> [--remember]` and applies the
// add via permissions.ApplyPermissionUpdate, optionally persisting
// to .biumind/settings.local.json.
func (m model) handleAddDir(parts []string) string {
	if m.engine == nil {
		return "/add-dir: engine not wired in this session"
	}
	ctx := m.engine.Permissions()
	if ctx == nil {
		return "/add-dir: permission context unavailable"
	}

	pathArg, remember, err := parseAddDirArgs(parts)
	if err != nil {
		return fmt.Sprintf("/add-dir: %s\nusage: /add-dir <path> [--remember]", err)
	}

	cwd, _ := os.Getwd()
	result := permissions.ValidateDirectoryForWorkspace(pathArg, ctx, cwd)
	switch result.Kind {
	case permissions.DirValidEmpty,
		permissions.DirValidPathNotFound,
		permissions.DirValidNotADirectory:
		return "/add-dir: " + result.HelpMessage()
	case permissions.DirValidAlreadyInWorkingDir:
		return "/add-dir: " + result.HelpMessage()
	case permissions.DirValidSuccess:
		// fall through
	}

	dest := sdkproto.PermissionDestSession
	if remember {
		dest = sdkproto.PermissionDestLocalSettings
	}
	update := &sdkproto.AddDirectories{
		Type:        sdkproto.PermissionUpdateAddDirectories,
		Directories: []string{result.AbsolutePath},
		Destination: dest,
	}
	if err := permissions.ApplyPermissionUpdate(ctx, update); err != nil {
		return fmt.Sprintf("/add-dir: apply failed: %v", err)
	}

	msg := fmt.Sprintf("Added %s as a working directory for this session", result.AbsolutePath)
	if remember {
		if err := clauseSettings.PersistPermissionUpdate(cwd, update); err != nil {
			msg = fmt.Sprintf(
				"Added %s as a working directory; persistence to .biumind/settings.local.json failed: %v",
				result.AbsolutePath, err)
		} else {
			msg = fmt.Sprintf(
				"Added %s as a working directory and saved to .biumind/settings.local.json",
				result.AbsolutePath)
		}
	}
	return msg + "  · /permissions to manage · /remove-dir to undo"
}

// handleRemoveDir parses `/remove-dir <path>` and removes the dir
// from the runtime ctx. If the path's source is a settings layer
// (and the layer is writable from cwd), the change is persisted.
func (m model) handleRemoveDir(parts []string) string {
	if m.engine == nil {
		return "/remove-dir: engine not wired in this session"
	}
	ctx := m.engine.Permissions()
	if ctx == nil {
		return "/remove-dir: permission context unavailable"
	}
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return "/remove-dir: missing path\nusage: /remove-dir <path>"
	}
	raw := strings.TrimSpace(strings.Join(parts[1:], " "))

	// Resolve to absolute exactly the way ValidateDirectoryForWorkspace
	// did at add time so the in-memory map key matches.
	cwd, _ := os.Getwd()
	result := permissions.ValidateDirectoryForWorkspace(raw, ctx, cwd)
	// "AlreadyInWorkingDir" + "Success" both yield a usable
	// AbsolutePath / ExistingWorkingDir. For removal we accept any
	// validated absolute even if not literally registered (the
	// caller's input may not be the exact registered key) — we just
	// pass it through and let the ctx no-op.
	target := result.AbsolutePath
	if target == "" {
		target = result.ExistingWorkingDir
	}
	if target == "" {
		// Last resort: use the literal input. Won't match if it's
		// relative, but emit a friendly error.
		return fmt.Sprintf("/remove-dir: cannot resolve path %q", raw)
	}

	src, known := ctx.DirectorySource(target)
	if !known {
		return fmt.Sprintf("/remove-dir: %s is not a registered working directory", target)
	}

	update := &sdkproto.RemoveDirectories{
		Type:        sdkproto.PermissionUpdateRemoveDirectories,
		Directories: []string{target},
		Destination: destinationForSource(src),
	}
	if err := permissions.ApplyPermissionUpdate(ctx, update); err != nil {
		return fmt.Sprintf("/remove-dir: apply failed: %v", err)
	}

	persistedNote := ""
	if dest := destinationForSource(src); clauseSettings.SupportsPersistence(dest) {
		if err := clauseSettings.PersistPermissionUpdate(cwd, update); err != nil {
			persistedNote = fmt.Sprintf(" (settings file write failed: %v)", err)
		} else {
			persistedNote = fmt.Sprintf(" (also removed from %s)", settingsLayerLabel(src))
		}
	}
	return fmt.Sprintf("Removed %s from working directories%s", target, persistedNote)
}

// parseAddDirArgs extracts the path and the --remember flag from the
// raw slash parts array (parts[0] is the slash itself). The flag may
// appear in any position; the path is the first non-flag token.
func parseAddDirArgs(parts []string) (path string, remember bool, err error) {
	if len(parts) < 2 {
		return "", false, fmt.Errorf("missing path")
	}
	pathTokens := make([]string, 0, len(parts)-1)
	for _, tok := range parts[1:] {
		switch tok {
		case "--remember", "--persist", "--save":
			remember = true
		default:
			pathTokens = append(pathTokens, tok)
		}
	}
	path = strings.TrimSpace(strings.Join(pathTokens, " "))
	if path == "" {
		return "", false, fmt.Errorf("missing path")
	}
	return path, remember, nil
}

// destinationForSource maps a runtime Source back to the wire-format
// destination string PersistPermissionUpdate expects. Mirrors
// permissions.SourceFromDestination but inverted.
func destinationForSource(s permissions.Source) string {
	switch s {
	case permissions.SrcLocalSettings:
		return sdkproto.PermissionDestLocalSettings
	case permissions.SrcUserSettings:
		return sdkproto.PermissionDestUserSettings
	case permissions.SrcProjectSettings:
		return sdkproto.PermissionDestProjectSettings
	case permissions.SrcCLIArg:
		return sdkproto.PermissionDestCliArg
	}
	return sdkproto.PermissionDestSession
}

// settingsLayerLabel renders a human-readable label for a settings
// source. Used by /remove-dir's confirmation note.
func settingsLayerLabel(s permissions.Source) string {
	switch s {
	case permissions.SrcUserSettings:
		return "~/.biumind/settings.json"
	case permissions.SrcProjectSettings:
		return ".biumind/settings.json"
	case permissions.SrcLocalSettings:
		return ".biumind/settings.local.json"
	}
	return string(s)
}
