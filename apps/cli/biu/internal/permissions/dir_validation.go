// Workspace-directory validation for /add-dir, --add-dir, and the
// settings-loading path. Produces five terminal states, with one
// twist: biumind treats EACCES / EPERM as "pathNotFound", so a
// settings-configured dir on an inaccessible volume does not crash
// startup.
//
// Returns the canonical path on success so the caller can
// ApplyPermissionUpdate / persist the EXACT string downstream
// consumers will compare against (post-`~` expansion, post-Abs).

package permissions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DirValidationKind classifies the outcome of validating a
// candidate working-directory string.
type DirValidationKind string

const (
	DirValidEmpty                  DirValidationKind = "emptyPath"
	DirValidPathNotFound           DirValidationKind = "pathNotFound"
	DirValidNotADirectory          DirValidationKind = "notADirectory"
	DirValidAlreadyInWorkingDir    DirValidationKind = "alreadyInWorkingDirectory"
	DirValidSuccess                DirValidationKind = "success"
)

// DirValidationResult is one of the five terminal states. Fields are
// populated only when relevant to the kind:
//
//   Empty:                {Kind}
//   PathNotFound:         {Kind, Input, AbsolutePath}
//   NotADirectory:        {Kind, Input, AbsolutePath}
//   AlreadyInWorkingDir:  {Kind, Input, ExistingWorkingDir}
//   Success:              {Kind, Input, AbsolutePath}
type DirValidationResult struct {
	Kind               DirValidationKind
	Input              string
	AbsolutePath       string
	ExistingWorkingDir string
}

// HelpMessage renders a one-line user-facing string for the result.
// UI-agnostic so both the slash command and the CLI flag warning code
// can use the same wording.
func (r DirValidationResult) HelpMessage() string {
	switch r.Kind {
	case DirValidEmpty:
		return "Please provide a directory path."
	case DirValidPathNotFound:
		return fmt.Sprintf("Path %s was not found.", r.AbsolutePath)
	case DirValidNotADirectory:
		parent := filepath.Dir(r.AbsolutePath)
		return fmt.Sprintf("%s is not a directory. Did you mean to add the parent directory %s?",
			r.Input, parent)
	case DirValidAlreadyInWorkingDir:
		return fmt.Sprintf("%s is already accessible within the existing working directory %s.",
			r.Input, r.ExistingWorkingDir)
	case DirValidSuccess:
		return fmt.Sprintf("Added %s as a working directory.", r.AbsolutePath)
	}
	return ""
}

// ValidateDirectoryForWorkspace checks whether path can be added as
// an additional working directory in ctx (with the given originalCwd
// as the implicit primary working directory).
//
// Order of checks:
//   1. Empty path → DirValidEmpty
//   2. Resolve absolute (~ expansion + Abs)
//   3. Stat: missing / EACCES / EPERM → DirValidPathNotFound
//             (a non-existent path is a config-time mistake, not a
//             reason to crash startup)
//   4. Stat: not a directory → DirValidNotADirectory
//   5. Containment: already inside any working dir →
//      DirValidAlreadyInWorkingDir (returns the matching dir)
//   6. otherwise DirValidSuccess
//
// ctx may be nil (caller has no permission state yet during early
// startup); only the cwd containment check is then performed.
func ValidateDirectoryForWorkspace(input string, ctx *Context, originalCwd string) DirValidationResult {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return DirValidationResult{Kind: DirValidEmpty, Input: input}
	}

	abs, ok := absExpand(trimmed)
	if !ok {
		// absExpand failed — treat as not-found rather than introducing
		// a sixth error kind. The user sees "path not found".
		return DirValidationResult{
			Kind:         DirValidPathNotFound,
			Input:        input,
			AbsolutePath: trimmed,
		}
	}

	info, err := os.Stat(abs)
	if err != nil {
		// ENOENT, ENOTDIR, EACCES, EPERM → all treated as "not found".
		// Distinguishing wouldn't help the user — they still need to
		// fix the path either way.
		if errors.Is(err, fs.ErrNotExist) ||
			errors.Is(err, fs.ErrPermission) ||
			isENOTDIR(err) {
			return DirValidationResult{
				Kind:         DirValidPathNotFound,
				Input:        input,
				AbsolutePath: abs,
			}
		}
		// Unexpected OS error: still treat as not-found, same fail-safe.
		return DirValidationResult{
			Kind:         DirValidPathNotFound,
			Input:        input,
			AbsolutePath: abs,
		}
	}
	if !info.IsDir() {
		return DirValidationResult{
			Kind:         DirValidNotADirectory,
			Input:        input,
			AbsolutePath: abs,
		}
	}

	// Containment check: is abs already inside a working dir we know?
	// We use PathInWorkingPath, NOT AdditionalDirectoryPaths exact
	// match, so adding /repo/sub when /repo is already a working dir
	// is reported as "already in".
	for _, w := range AllWorkingDirectories(ctx, originalCwd) {
		if PathInWorkingPath(abs, w) {
			return DirValidationResult{
				Kind:               DirValidAlreadyInWorkingDir,
				Input:              input,
				ExistingWorkingDir: w,
			}
		}
	}

	return DirValidationResult{
		Kind:         DirValidSuccess,
		Input:        input,
		AbsolutePath: abs,
	}
}

// isENOTDIR reports whether err is a "not a directory" syscall error.
// fs.ErrNotExist covers ENOENT but not ENOTDIR; we want both treated
// the same (the user typed a path that doesn't resolve to a dir).
func isENOTDIR(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		// Match the textual error since Go's syscall.Errno comparison
		// requires importing syscall on every OS — keeping this string
		// match is portable and the cost of a false positive
		// (rejecting a malformed path message) is acceptable.
		msg := pathErr.Err.Error()
		if strings.Contains(msg, "not a directory") {
			return true
		}
	}
	return false
}
