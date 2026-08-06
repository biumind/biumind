// Linux Secret Service via the `secret-tool` CLI (libsecret-tools).
// Extracted from the original oauth linux backend.

//go:build linux

package oskeychain

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
)

func openPlatform() (Keychain, bool) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return nil, false
	}
	// libsecret needs a D-Bus session to reach the keyring daemon. No
	// session vars → almost certainly headless → fall back to file.
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		return nil, false
	}
	return linuxSecretService{}, true
}

type linuxSecretService struct{}

func (linuxSecretService) Name() string { return "secret-service" }

func (linuxSecretService) Get(service, account string) (string, bool, error) {
	out, err := runSecretTool("lookup", "service", service, "account", account)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && len(out) == 0 {
			return "", false, nil // lookup miss
		}
		return "", false, err
	}
	return string(bytes.TrimRight(out, "\n")), true, nil
}

func (linuxSecretService) Set(service, account, value string) error {
	cmd := exec.Command("secret-tool", "store",
		"--label=biu agent-plane secret",
		"service", service, "account", account)
	cmd.Stdin = strings.NewReader(value)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return errors.New("secret-tool: " + strings.TrimSpace(stderr.String()))
		}
		return err
	}
	return nil
}

func (linuxSecretService) Delete(service, account string) error {
	_, err := runSecretTool("clear", "service", service, "account", account)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil // nothing to delete
		}
	}
	return err
}

func runSecretTool(args ...string) ([]byte, error) {
	cmd := exec.Command("secret-tool", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}
