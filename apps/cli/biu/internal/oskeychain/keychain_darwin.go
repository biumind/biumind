// macOS Keychain via the `security` CLI. Extracted from the original
// oauth darwin backend so both oauth and secretstore share one impl.

//go:build darwin

package oskeychain

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

func openPlatform() (Keychain, bool) {
	if _, err := exec.LookPath("security"); err != nil {
		return nil, false
	}
	return darwinKeychain{}, true
}

type darwinKeychain struct{}

func (darwinKeychain) Name() string { return "darwin-keychain" }

func (darwinKeychain) Get(service, account string) (string, bool, error) {
	// `-w` writes the password to stdout.
	out, err := runQuiet("security", "find-generic-password",
		"-s", service, "-a", account, "-w")
	if err != nil {
		if isNotFoundExitCode(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(bytes.TrimRight(out, "\n")), true, nil
}

func (darwinKeychain) Set(service, account, value string) error {
	// `-U` updates if exists. `-T ""` restricts the ACL so only the
	// `security` binary (and biu shelling out to it) reads without prompt.
	_, err := runQuiet("security", "add-generic-password",
		"-U", "-s", service, "-a", account, "-w", value, "-T", "")
	return err
}

func (darwinKeychain) Delete(service, account string) error {
	_, err := runQuiet("security", "delete-generic-password",
		"-s", service, "-a", account)
	if err != nil && isNotFoundExitCode(err) {
		return nil
	}
	return err
}

// runQuiet runs cmd, returns stdout, and wraps non-zero exits with the
// exit code so isNotFoundExitCode can recognise errSecItemNotFound (44).
func runQuiet(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, &keychainExitError{
				ExitCode: ee.ExitCode(),
				Stderr:   strings.TrimSpace(stderr.String()),
				Inner:    err,
			}
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

type keychainExitError struct {
	ExitCode int
	Stderr   string
	Inner    error
}

func (e *keychainExitError) Error() string {
	if e.Stderr != "" {
		return "security: " + e.Stderr
	}
	return e.Inner.Error()
}

func (e *keychainExitError) Unwrap() error { return e.Inner }

// isNotFoundExitCode recognises macOS `security` reporting "no item":
// errSecItemNotFound = -25300 surfaces as CLI exit code 44.
func isNotFoundExitCode(err error) bool {
	var ke *keychainExitError
	if errors.As(err, &ke) {
		return ke.ExitCode == 44
	}
	return false
}
