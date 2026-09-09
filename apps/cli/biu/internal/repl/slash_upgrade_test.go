package repl

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
)

func TestHandleUpgrade_bareShowsCommand(t *testing.T) {
	got := model{}.handleUpgrade([]string{"/upgrade"})
	for _, want := range []string{"biu ", "install method:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestHandleUpgrade_checkDoesNotRun(t *testing.T) {
	// `check` is dispatched async in model.go; if it ever reaches
	// handleUpgrade synchronously it must at least not execute the
	// upgrade command.
	got := model{}.handleUpgrade([]string{"/upgrade", "check"})
	if strings.Contains(got, "running…") {
		t.Errorf("check should not run: %s", got)
	}
}

func TestHandleUpgradeCheckSkip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// No version known yet → guidance, not an error.
	got := model{}.handleUpgradeCheckSkip(nil)
	if !strings.Contains(got, "nothing to skip") {
		t.Errorf("empty state should guide to /upgrade check first, got: %s", got)
	}

	// Explicit version → recorded.
	got = model{}.handleUpgradeCheckSkip([]string{"0.4.1"})
	if !strings.Contains(got, "0.4.1") {
		t.Errorf("explicit skip should echo the version, got: %s", got)
	}
	state, err := updatecheck.LoadState("")
	if err != nil {
		t.Fatal(err)
	}
	if state.SkippedVersion != "0.4.1" {
		t.Errorf("skipped version not persisted, got %+v", state)
	}

	// Bare form picks up the latest known version from state.
	if err := updatecheck.RecordCheck("0.5.0"); err != nil {
		t.Fatal(err)
	}
	got = model{}.handleUpgradeCheckSkip(nil)
	if !strings.Contains(got, "0.5.0") {
		t.Errorf("bare skip should use latest known version, got: %s", got)
	}
}

func TestRunUpgradeCommand_rejectsShellMetachars(t *testing.T) {
	cases := []string{
		"echo hi; rm -rf /",
		"brew upgrade biu | tee /tmp/x",
		"go install foo > /dev/null",
		"`echo bad`",
	}
	for _, c := range cases {
		_, err := runUpgradeCommand(c)
		if err == nil || !strings.Contains(err.Error(), "metachars") {
			t.Errorf("metachar %q should be rejected, got err=%v", c, err)
		}
	}
}

func TestRunUpgradeCommand_emptyErrors(t *testing.T) {
	_, err := runUpgradeCommand("   ")
	if err == nil {
		t.Error("empty command should error")
	}
}
